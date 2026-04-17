// Fujifilm camera session — high-level PTP operations.
// Port of filmkit/src/ptp/session.ts.
// Concurrency model: callers must hold the camera mutex (managed by api/server.go).
package ptp

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"filmkit-daemon/internal/util"
)

// DeviceInfo holds parsed PTP GetDeviceInfo response.
type DeviceInfo struct {
	Model         string
	SerialNumber  string
	Manufacturer  string
	DeviceVersion string
	Properties    []uint16
	Operations    []uint16
}

// RawProp holds a single camera property value with its raw bytes.
type RawProp struct {
	ID    uint16
	Name  string
	Bytes []byte
	Value any // int16, int32, uint32, or string
}

// PresetData holds a complete preset slot read from the camera.
type PresetData struct {
	Slot     int
	Name     string
	Settings []RawProp
}

// Camera manages a PTP session with a Fujifilm camera.
type Camera struct {
	transport           *Transport
	log                 func(string)
	sessionOpen         bool
	ModelName           string
	SupportedProperties map[uint16]bool
	BaseProfile         []byte
	RAFLoaded           bool
}

// NewCamera creates a Camera with the given log function.
func NewCamera(logFn func(string)) *Camera {
	if logFn == nil {
		logFn = func(s string) { log.Println(s) }
	}
	return &Camera{
		transport:           NewTransport(logFn),
		log:                 logFn,
		SupportedProperties: make(map[uint16]bool),
	}
}

// Connected returns true if the USB device is open.
func (c *Camera) Connected() bool {
	return c.transport.Connected()
}

// ProductName returns the USB product name from the device descriptor.
func (c *Camera) ProductName() string {
	return c.transport.ProductName()
}

// Connect opens the USB device and starts a PTP session.
func (c *Camera) Connect() error {
	if err := c.transport.Connect(); err != nil {
		return err
	}
	if err := c.openSession(); err != nil {
		c.transport.Close()
		return err
	}
	// Fetch device info — non-fatal if it fails
	info, err := c.GetDeviceInfo()
	if err == nil {
		c.ModelName = info.Model
		for _, p := range info.Properties {
			c.SupportedProperties[p] = true
		}
		c.log(fmt.Sprintf("Camera: %s (%d properties)", c.ModelName, len(info.Properties)))
	} else {
		c.ModelName = c.transport.ProductName()
	}
	return nil
}

// Disconnect closes the PTP session and releases USB.
func (c *Camera) Disconnect() {
	if c.sessionOpen {
		_ = c.closeSession()
	}
	c.transport.Close()
	c.RAFLoaded = false
	c.BaseProfile = nil
}

func (c *Camera) openSession() error {
	c.log("Opening PTP session...")
	code, _, _, err := c.transport.SendCommand(OpOpenSession, []uint32{1}, 0)
	if err != nil {
		return fmt.Errorf("OpenSession: %w", err)
	}
	if code == RespOK {
		c.sessionOpen = true
		c.log("PTP session opened")
		return nil
	}
	if code == RespSessionAlreadyOpen {
		// Stale session from a previous connection — close it and reset USB.
		c.log("Stale session detected — closing and resetting...")
		_, _, _, _ = c.transport.SendCommand(OpCloseSession, nil, 0)
		if err := c.transport.Reset(); err != nil {
			return fmt.Errorf("transport reset after stale session: %w", err)
		}
		code, _, _, err = c.transport.SendCommand(OpOpenSession, []uint32{1}, 0)
		if err != nil {
			return fmt.Errorf("OpenSession retry: %w", err)
		}
		if code == RespOK {
			c.sessionOpen = true
			c.log("PTP session opened (after reset)")
			return nil
		}
	}
	return fmt.Errorf("OpenSession failed: %s", RespName(code))
}

func (c *Camera) closeSession() error {
	c.log("Closing PTP session...")
	code, _, _, err := c.transport.SendCommand(OpCloseSession, nil, 0)
	c.sessionOpen = false
	if err != nil {
		return err
	}
	if code != RespOK {
		return fmt.Errorf("CloseSession: %s", RespName(code))
	}
	c.log("PTP session closed")
	return nil
}

// GetDeviceInfo reads PTP DeviceInfo (operations, properties, model, etc.).
func (c *Camera) GetDeviceInfo() (DeviceInfo, error) {
	code, _, data, err := c.transport.SendCommand(OpGetDeviceInfo, nil, 0)
	if err != nil {
		return DeviceInfo{}, err
	}
	if code != RespOK {
		return DeviceInfo{}, fmt.Errorf("GetDeviceInfo: %s", RespName(code))
	}

	r := util.NewPTPReader(data)
	r.U16()      // StandardVersion
	r.U32()      // VendorExtensionID
	r.U16()      // VendorExtensionVersion
	r.Str()      // VendorExtensionDesc
	r.U16()      // FunctionalMode

	opsRaw := r.U16Array()
	r.U16Array() // EventsSupported
	propsRaw := r.U16Array()
	r.U16Array() // CaptureFormats
	r.U16Array() // ImageFormats

	manufacturer := r.Str()
	model := r.Str()
	deviceVersion := r.Str()
	serialNumber := r.Str()

	ops := make([]uint16, len(opsRaw))
	for i, v := range opsRaw {
		ops[i] = v
	}
	props := make([]uint16, len(propsRaw))
	for i, v := range propsRaw {
		props[i] = v
	}

	return DeviceInfo{
		Model:         model,
		SerialNumber:  serialNumber,
		Manufacturer:  manufacturer,
		DeviceVersion: deviceVersion,
		Properties:    props,
		Operations:    ops,
	}, nil
}

// decodePropValue smart-decodes raw property bytes to a usable value.
// Mirrors the decodePropValue function in session.ts.
func decodePropValue(data []byte) any {
	// Heuristic: if it looks like a PTP string, parse as string
	if len(data) >= 3 {
		numChars := int(data[0])
		expected := 1 + numChars*2
		if numChars >= 2 && (expected == len(data) || expected == len(data)+1) {
			r := util.NewPTPReader(data)
			return r.Str()
		}
	}
	switch len(data) {
	case 1:
		return int(data[0])
	case 2:
		return int(int16(binary.LittleEndian.Uint16(data)))
	case 4:
		return int(int32(binary.LittleEndian.Uint32(data)))
	}
	// Hex fallback for longer values
	hex := "0x"
	for i, b := range data {
		if i >= 16 {
			break
		}
		hex += fmt.Sprintf("%02x", b)
	}
	return hex
}

// ReadProp reads a single property value via GetDevicePropValue.
func (c *Camera) ReadProp(propID uint16) (*RawProp, error) {
	code, _, data, err := c.transport.SendCommand(OpGetDevicePropValue, []uint32{uint32(propID)}, 0)
	if err != nil {
		return nil, err
	}
	if code != RespOK || len(data) == 0 {
		return nil, nil //nolint // property not supported or empty
	}
	return &RawProp{
		ID:    propID,
		Name:  PropName(propID),
		Bytes: data,
		Value: decodePropValue(data),
	}, nil
}

// WritePropU16 writes a uint16 value to a device property.
func (c *Camera) WritePropU16(propID, value uint16) error {
	code, _, err := c.transport.SendDataCommand(
		OpSetDevicePropValue, []uint32{uint32(propID)},
		util.PackU16(value), 0,
	)
	if err != nil {
		return err
	}
	if code != RespOK {
		return fmt.Errorf("SetDevicePropValue(0x%04X): %s", propID, RespName(code))
	}
	return nil
}

// WritePropRaw writes raw bytes to a device property.
func (c *Camera) WritePropRaw(propID uint16, payload []byte) error {
	code, _, err := c.transport.SendDataCommand(
		OpSetDevicePropValue, []uint32{uint32(propID)},
		payload, 0,
	)
	if err != nil {
		return err
	}
	if code != RespOK {
		return fmt.Errorf("SetDevicePropValue(0x%04X): %s", propID, RespName(code))
	}
	return nil
}

// WritePropString writes a PTP-encoded string to a device property.
func (c *Camera) WritePropString(propID uint16, value string) error {
	return c.WritePropRaw(propID, util.PackPTPString(value))
}

// ScanPresets reads all 7 custom preset slots (C1–C7) from the camera.
func (c *Camera) ScanPresets() ([]PresetData, error) {
	// Check if camera supports preset properties
	if len(c.SupportedProperties) > 0 && !c.SupportedProperties[PropPresetSlot] {
		c.log("Camera does not support preset properties (D18C)")
		return nil, nil
	}

	// Save current slot
	origProp, _ := c.ReadProp(PropPresetSlot)
	origSlot := uint16(1)
	if origProp != nil {
		if v, ok := origProp.Value.(int); ok {
			origSlot = uint16(v)
		}
	}

	var presets []PresetData

	for slot := 1; slot <= 7; slot++ {
		if err := c.WritePropU16(PropPresetSlot, uint16(slot)); err != nil {
			c.log(fmt.Sprintf("Slot %d: select failed — %v", slot, err))
			if slot == 1 {
				c.log("Slot selection not supported — aborting preset scan")
				break
			}
			continue
		}

		time.Sleep(100 * time.Millisecond)

		nameProp, _ := c.ReadProp(PropPresetName)
		name := fmt.Sprintf("(slot %d)", slot)
		if nameProp != nil {
			if s, ok := nameProp.Value.(string); ok {
				name = s
			}
		}

		var settings []RawProp
		for pid := uint16(PropPresetFirst); pid <= PropPresetLast; pid++ {
			prop, err := c.ReadProp(pid)
			if err == nil && prop != nil {
				settings = append(settings, *prop)
			}
		}

		presets = append(presets, PresetData{Slot: slot, Name: name, Settings: settings})
		c.log(fmt.Sprintf("C%d: %q (%d settings)", slot, name, len(settings)))
	}

	// Restore original slot
	_ = c.WritePropU16(PropPresetSlot, origSlot)

	return presets, nil
}

// WritePreset writes a complete preset to a camera slot with verification.
// Returns a list of non-fatal warnings (e.g. read-only properties).
func (c *Camera) WritePreset(slot int, name string, settings []RawProp) (warnings []string, err error) {
	// 1. Select slot
	if err = c.WritePropU16(PropPresetSlot, uint16(slot)); err != nil {
		return nil, fmt.Errorf("select slot %d: %w", slot, err)
	}
	time.Sleep(100 * time.Millisecond)

	// 2. Write name
	if err = c.WritePropString(PropPresetName, name); err != nil {
		return nil, fmt.Errorf("write preset name: %w", err)
	}

	// 3. Write all settings
	written := make(map[uint16]bool)
	for _, s := range settings {
		if werr := c.WritePropRaw(s.ID, s.Bytes); werr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: write rejected", PropName(s.ID)))
		} else {
			written[s.ID] = true
		}
	}

	// 4. Verify name
	verifyName, _ := c.ReadProp(PropPresetName)
	if verifyName != nil {
		if vs, ok := verifyName.Value.(string); ok && vs != name {
			return warnings, fmt.Errorf("name verify failed: wrote %q read %q", name, vs)
		}
	}

	// 5. Verify written properties
	for _, s := range settings {
		if !written[s.ID] {
			continue
		}
		rb, verr := c.ReadProp(s.ID)
		if verr != nil || rb == nil {
			continue
		}
		if len(s.Bytes) == len(rb.Bytes) {
			for i := range s.Bytes {
				if s.Bytes[i] != rb.Bytes[i] {
					return warnings, fmt.Errorf("%s: verify mismatch after write", PropName(s.ID))
				}
			}
		}
	}

	c.log(fmt.Sprintf("Slot %d: wrote %d/%d properties", slot, len(written), len(settings)))
	return warnings, nil
}

// SendRAF uploads a RAF file to the camera using Fuji vendor PTP commands.
func (c *Camera) SendRAF(data []byte) error {
	sizeMB := float64(len(data)) / 1024 / 1024
	c.log(fmt.Sprintf("Uploading RAF (%.1f MB)...", sizeMB))

	// Build PTP ObjectInfo structure
	objectInfo := buildObjectInfo(len(data))

	// Step 1: SendObjectInfo (Fuji vendor 0x900C)
	code, _, err := c.transport.SendDataCommand(
		FujiOpSendObjectInfo, []uint32{0, 0, 0}, objectInfo, 0,
	)
	if err != nil {
		return fmt.Errorf("SendObjectInfo: %w", err)
	}
	if code != RespOK {
		return fmt.Errorf("SendObjectInfo: %s", RespName(code))
	}

	// Step 2: SendObject2 (Fuji vendor 0x900D) — large file, 60s timeout
	code, _, err = c.transport.SendDataCommand(
		FujiOpSendObject2, nil, data, 60*time.Second,
	)
	if err != nil {
		return fmt.Errorf("SendObject2: %w", err)
	}
	if code != RespOK {
		return fmt.Errorf("SendObject2: %s", RespName(code))
	}

	c.log("RAF uploaded")
	return nil
}

// GetProfile reads the RAW conversion profile (property 0xD185) from the camera.
// A RAF must be loaded first.
func (c *Camera) GetProfile() ([]byte, error) {
	c.log("Reading D185 profile...")
	code, _, data, err := c.transport.SendCommand(OpGetDevicePropValue, []uint32{uint32(PropRawConvProfile)}, 0)
	if err != nil {
		return nil, err
	}
	if code != RespOK {
		return nil, fmt.Errorf("GetDevicePropValue(D185): %s", RespName(code))
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("D185 profile empty — is a RAF file loaded?")
	}
	c.log(fmt.Sprintf("Profile received: %d bytes", len(data)))
	return data, nil
}

// SetProfile writes a modified conversion profile (property 0xD185) to the camera.
func (c *Camera) SetProfile(profile []byte) error {
	c.log(fmt.Sprintf("Writing D185 profile (%d bytes)...", len(profile)))
	code, _, err := c.transport.SendDataCommand(
		OpSetDevicePropValue, []uint32{uint32(PropRawConvProfile)}, profile, 0,
	)
	if err != nil {
		return err
	}
	if code != RespOK {
		return fmt.Errorf("SetDevicePropValue(D185): %s", RespName(code))
	}
	return nil
}

// TriggerConversion sets property 0xD183=0 to start RAW conversion.
func (c *Camera) TriggerConversion() error {
	c.log("Triggering RAW conversion...")
	code, _, err := c.transport.SendDataCommand(
		OpSetDevicePropValue, []uint32{uint32(PropStartRawConversion)},
		util.PackU16(0), 0,
	)
	if err != nil {
		return err
	}
	if code != RespOK {
		return fmt.Errorf("StartRawConversion: %s", RespName(code))
	}
	return nil
}

// drainResults deletes any existing conversion result objects from the camera.
// Called before triggering a new conversion to prevent stale handles from being
// returned immediately by the next WaitForResult call.
func (c *Camera) drainResults() {
	for i := 0; i < 10; i++ {
		code, _, data, err := c.transport.SendCommand(
			OpGetObjectHandles, []uint32{0xFFFFFFFF, 0x0000, 0x00000000}, 0,
		)
		if err != nil || code != RespOK || len(data) < 4 {
			return
		}
		numHandles := binary.LittleEndian.Uint32(data[0:4])
		if numHandles == 0 {
			return
		}
		handle := binary.LittleEndian.Uint32(data[4:8])
		c.log(fmt.Sprintf("drainResults: removing stale handle 0x%08X", handle))
		delCode, _, _, _ := c.transport.SendCommand(OpDeleteObject, []uint32{handle}, 0)
		if delCode != RespOK {
			c.log(fmt.Sprintf("drainResults: DeleteObject returned %s — will skip", RespName(delCode)))
			return // can't delete, stop trying
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// snapshotHandles returns the set of all currently-known object handles.
// Used before TriggerConversion so WaitForResult can skip pre-existing objects
// (e.g. the uploaded RAF file) and only return the newly-created JPEG result.
func (c *Camera) snapshotHandles() map[uint32]bool {
	handles := make(map[uint32]bool)
	code, _, data, err := c.transport.SendCommand(
		OpGetObjectHandles, []uint32{0xFFFFFFFF, 0x0000, 0x00000000}, 0,
	)
	if err != nil || code != RespOK || len(data) < 4 {
		return handles
	}
	n := binary.LittleEndian.Uint32(data[0:4])
	for i := uint32(0); i < n; i++ {
		if off := 4 + i*4; int(off+4) <= len(data) {
			handles[binary.LittleEndian.Uint32(data[off:])] = true
		}
	}
	c.log(fmt.Sprintf("snapshotHandles: %d pre-existing handle(s)", len(handles)))
	return handles
}

// WaitForResult polls GetObjectHandles until a newly-converted JPEG appears,
// downloads it, then deletes the temp object from the camera.
// skip: set of pre-existing handles to ignore (pass nil to skip nothing).
func (c *Camera) WaitForResult(timeout time.Duration) ([]byte, error) {
	return c.waitForResult(timeout, nil)
}

func (c *Camera) waitForResult(timeout time.Duration, skip map[uint32]bool) ([]byte, error) {
	c.log("Waiting for conversion result...")
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		code, _, data, err := c.transport.SendCommand(
			OpGetObjectHandles, []uint32{0xFFFFFFFF, 0x0000, 0x00000000}, 0,
		)
		if err != nil {
			return nil, err
		}
		if code != RespOK {
			return nil, fmt.Errorf("GetObjectHandles: %s", RespName(code))
		}

		if len(data) >= 8 {
			numHandles := binary.LittleEndian.Uint32(data[0:4])
			// Scan all returned handles for one that isn't in the skip set
			var handle uint32
			for i := uint32(0); i < numHandles; i++ {
				if off := 4 + i*4; int(off+4) <= len(data) {
					h := binary.LittleEndian.Uint32(data[off:])
					if skip == nil || !skip[h] {
						handle = h
						break
					}
				}
			}
			if handle != 0 {
				c.log(fmt.Sprintf("Conversion done (handle=0x%08X)", handle))

				// Download JPEG
				code, _, jpeg, err := c.transport.SendCommand(
					OpGetObject, []uint32{handle}, 60*time.Second,
				)
				if err != nil {
					return nil, err
				}
				if code != RespOK {
					return nil, fmt.Errorf("GetObject: %s", RespName(code))
				}

				// Delete result object — log failure but don't abort
				delCode, _, _, _ := c.transport.SendCommand(OpDeleteObject, []uint32{handle}, 0)
				if delCode != RespOK {
					c.log(fmt.Sprintf("WaitForResult: DeleteObject(0x%08X) returned %s", handle, RespName(delCode)))
				}

				c.log(fmt.Sprintf("Downloaded %.1f MB JPEG", float64(len(jpeg))/1024/1024))
				return jpeg, nil
			}
		}

		time.Sleep(time.Second)
	}

	return nil, fmt.Errorf("conversion timeout after %v", timeout)
}


// SendRAFStream uploads a RAF file from an io.Reader to avoid buffering in RAM.
// size must be the exact byte length of the RAF.
//
// The camera requires the FULL file to be sent in a single PTP DATA phase —
// partial SendObject2 calls (tested with 1MB chunks) receive no response.
// The camera buffers ~3MB in USB receive hardware, then writes to SD card.
// First write takes ~54s (workspace initialisation); subsequent writes at full speed.
// We use WriteContext per-chunk with a long timeout to ride out the initial pause.
func (c *Camera) SendRAFStream(r io.Reader, size int64) error {
	c.log(fmt.Sprintf("Uploading RAF (%.1f MB) as single stream...", float64(size)/1024/1024))

	objectInfo := buildObjectInfo(int(size))

	// Step 1: SendObjectInfo (Fuji vendor 0x900C) — declares the full file size.
	code, _, err := c.transport.SendDataCommand(
		FujiOpSendObjectInfo, []uint32{0, 0, 0}, objectInfo, 0,
	)
	if err != nil {
		return fmt.Errorf("SendObjectInfo: %w", err)
	}
	if code != RespOK {
		return fmt.Errorf("SendObjectInfo: %s", RespName(code))
	}

	// Step 2: Stream the full RAF in one DATA phase.
	// WriteContext per chunk: 10 minutes (camera can pause ~54s after first 3MB fills buffer).
	// recv timeout: 10 minutes for the RESPONSE after all data is written.
	code, _, err = c.transport.SendDataCommandFromReader(
		FujiOpSendObject2, nil, size, r, 10*time.Minute,
	)
	if err != nil {
		return fmt.Errorf("SendObject2: %w", err)
	}
	if code != RespOK {
		return fmt.Errorf("SendObject2: %s", RespName(code))
	}

	c.log("RAF uploaded successfully")
	return nil
}

// LoadRAFStream uploads a RAF from an io.Reader, reads the base profile, and returns the initial JPEG.
// Use this instead of LoadRAF to avoid OOM on routers with limited RAM.
func (c *Camera) LoadRAFStream(r io.Reader, size int64) ([]byte, error) {
	if err := c.SendRAFStream(r, size); err != nil {
		return nil, err
	}
	profile, err := c.GetProfile()
	if err != nil {
		return nil, err
	}
	c.BaseProfile = profile
	c.RAFLoaded = true

	if err := c.SetProfile(profile); err != nil {
		return nil, err
	}
	// Snapshot pre-existing handles (includes the uploaded RAF object).
	// WaitForResult will skip them and only return the newly-created JPEG result.
	preExisting := c.snapshotHandles()
	if err := c.TriggerConversion(); err != nil {
		return nil, err
	}
	// 5-minute timeout: X100VI 40MP RAW conversion can take >30s
	return c.waitForResult(5*time.Minute, preExisting)
}

// LoadRAF uploads a RAF, reads the base profile, and returns the initial JPEG.
func (c *Camera) LoadRAF(data []byte) ([]byte, error) {
	if err := c.SendRAF(data); err != nil {
		return nil, err
	}
	profile, err := c.GetProfile()
	if err != nil {
		return nil, err
	}
	c.BaseProfile = profile
	c.RAFLoaded = true

	if err := c.SetProfile(profile); err != nil {
		return nil, err
	}
	if err := c.TriggerConversion(); err != nil {
		return nil, err
	}
	return c.WaitForResult(30 * time.Second)
}

// logProfileAll dumps all params of the base profile and highlights diffs in modified.
func (c *Camera) logProfileAll(base, modified []byte) {
	if len(base) < 6 || len(base) != len(modified) {
		return
	}
	numParams := int(binary.LittleEndian.Uint16(base[0:2]))
	if numParams == 0 {
		return
	}
	off := len(base) - numParams*4
	if off < 2 || off+numParams*4 > len(base) {
		return
	}
	pb := func(idx int) int32 {
		return int32(binary.LittleEndian.Uint32(base[off+idx*4:]))
	}
	pm := func(idx int) int32 {
		return int32(binary.LittleEndian.Uint32(modified[off+idx*4:]))
	}
	// Log base profile (only once per RAF load ideally, but log every time for now)
	var baseParts []string
	for i := 0; i < numParams; i++ {
		baseParts = append(baseParts, fmt.Sprintf("[%d]=%d", i, pb(i)))
	}
	c.log("base profile: " + strings.Join(baseParts, " "))

	var diffs []string
	for i := 0; i < numParams; i++ {
		if pb(i) != pm(i) {
			diffs = append(diffs, fmt.Sprintf("[%d]:%d→%d", i, pb(i), pm(i)))
		}
	}
	if len(diffs) == 0 {
		c.log("profile diff: no changes")
	} else {
		c.log("profile diff: " + strings.Join(diffs, " "))
	}
}

// Reconvert applies a new profile to the already-loaded RAF and returns the JPEG.
func (c *Camera) Reconvert(profile []byte) ([]byte, error) {
	if !c.RAFLoaded || c.BaseProfile == nil {
		return nil, fmt.Errorf("no RAF loaded — call LoadRAF first")
	}
	c.logProfileAll(c.BaseProfile, profile)
	if err := c.SetProfile(profile); err != nil {
		return nil, err
	}
	// Snapshot pre-existing handles (stale results, uploaded RAF, etc.) so
	// waitForResult only returns the newly-created JPEG from THIS conversion.
	preExisting := c.snapshotHandles()
	if err := c.TriggerConversion(); err != nil {
		return nil, err
	}
	return c.waitForResult(5*time.Minute, preExisting)
}

// CameraFile holds basic metadata about a file stored on the camera's SD card.
type CameraFile struct {
	Handle    uint32
	Name      string
	SizeBytes uint32
}

// objectInfo holds raw PTP ObjectInfo fields we care about.
type objectInfo struct {
	handle    uint32
	name      string
	sizeBytes uint32
	format    uint16 // 0x3001 = Association (folder)
}

// ListRAFFiles returns all RAF files found on the camera's storage cards.
func (c *Camera) ListRAFFiles() ([]CameraFile, error) {
	// 1. GetStorageIDs
	code, _, data, err := c.transport.SendCommand(OpGetStorageIDs, nil, 0)
	if err != nil {
		return nil, err
	}
	if code != RespOK {
		return nil, fmt.Errorf("GetStorageIDs: %s", RespName(code))
	}
	if len(data) < 4 {
		return nil, fmt.Errorf("GetStorageIDs: short response")
	}
	numStorages := binary.LittleEndian.Uint32(data[0:4])
	storageIDs := make([]uint32, 0, numStorages)
	for i := uint32(0); i < numStorages; i++ {
		if off := 4 + i*4; int(off+4) <= len(data) {
			storageIDs = append(storageIDs, binary.LittleEndian.Uint32(data[off:]))
		}
	}

	seen := make(map[uint32]bool)
	var files []CameraFile
	for _, sid := range storageIDs {
		c.listRAFsInDir(sid, 0x00000000, 0, seen, &files)
	}
	c.log(fmt.Sprintf("ListRAFFiles: %d RAF file(s) found", len(files)))
	return files, nil
}

const ptpFormatAssociation = uint16(0x3001) // folder/directory
const maxDirDepth = 5

// listRAFsInDir recursively lists RAF files under a given parent handle.
// parentHandle=0x00000000 means root of storage.
// seen prevents duplicates when the camera reports the same handle in multiple dirs.
func (c *Camera) listRAFsInDir(storageID, parentHandle uint32, depth int, seen map[uint32]bool, out *[]CameraFile) {
	if depth > maxDirDepth {
		return
	}

	code, _, hdata, err := c.transport.SendCommand(
		OpGetObjectHandles, []uint32{storageID, 0x00000000, parentHandle}, 0,
	)
	if err != nil || code != RespOK || len(hdata) < 4 {
		return
	}

	numHandles := binary.LittleEndian.Uint32(hdata[0:4])
	if depth == 0 {
		c.log(fmt.Sprintf("Storage 0x%08X root: %d object(s)", storageID, numHandles))
	}

	for i := uint32(0); i < numHandles; i++ {
		off := 4 + i*4
		if int(off+4) > len(hdata) {
			break
		}
		handle := binary.LittleEndian.Uint32(hdata[off:])
		if seen[handle] {
			continue
		}
		seen[handle] = true

		info, err := c.getRawObjectInfo(handle)
		if err != nil {
			continue
		}

		if info.format == ptpFormatAssociation {
			c.log(fmt.Sprintf("  [dir] %q", info.name))
			c.listRAFsInDir(storageID, handle, depth+1, seen, out)
		} else {
			upper := strings.ToUpper(info.name)
			if strings.HasSuffix(upper, ".RAF") {
				c.log(fmt.Sprintf("  RAF: %q (%d bytes)", info.name, info.sizeBytes))
				*out = append(*out, CameraFile{Handle: handle, Name: info.name, SizeBytes: info.sizeBytes})
			}
		}
	}
}

// getRawObjectInfo fetches name, size, and format code for a single object handle.
func (c *Camera) getRawObjectInfo(handle uint32) (*objectInfo, error) {
	code, _, data, err := c.transport.SendCommand(OpGetObjectInfo, []uint32{handle}, 0)
	if err != nil {
		return nil, err
	}
	if code != RespOK {
		return nil, fmt.Errorf("GetObjectInfo: %s", RespName(code))
	}
	// ObjectInfo layout (byte offsets):
	//   0: StorageID (4), 4: ObjectFormat (2), 6: ProtectionStatus (2),
	//   8: CompressedSize (4), 12: ThumbFormat (2), 14: ThumbCompressedSize (4),
	//  18: ThumbPixWidth (4), 22: ThumbPixHeight (4), 26: ImagePixWidth (4),
	//  30: ImagePixHeight (4), 34: ImageBitDepth (4), 38: ParentObject (4),
	//  42: AssociationType (2), 44: AssociationDesc (4), 48: SequenceNumber (4),
	//  52: Filename (PTP string) …
	if len(data) < 52 {
		return nil, fmt.Errorf("GetObjectInfo: response too short (%d bytes)", len(data))
	}
	format := binary.LittleEndian.Uint16(data[4:6])
	size := binary.LittleEndian.Uint32(data[8:12])
	r := util.NewPTPReader(data[52:])
	name := r.Str()
	return &objectInfo{handle: handle, name: name, sizeBytes: size, format: format}, nil
}

// GetThumb retrieves the embedded JPEG thumbnail for an object handle.
func (c *Camera) GetThumb(handle uint32) ([]byte, error) {
	code, _, data, err := c.transport.SendCommand(OpGetThumb, []uint32{handle}, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if code != RespOK {
		return nil, fmt.Errorf("GetThumb(0x%08X): %s", handle, RespName(code))
	}
	return data, nil
}

// DownloadFile downloads a file by object handle from the camera (large timeout).
func (c *Camera) DownloadFile(handle uint32) ([]byte, error) {
	c.log(fmt.Sprintf("Downloading file 0x%08X...", handle))
	code, _, data, err := c.transport.SendCommand(OpGetObject, []uint32{handle}, 120*time.Second)
	if err != nil {
		return nil, err
	}
	if code != RespOK {
		return nil, fmt.Errorf("GetObject(0x%08X): %s", handle, RespName(code))
	}
	c.log(fmt.Sprintf("Downloaded %.1f MB", float64(len(data))/1024/1024))
	return data, nil
}

// buildObjectInfo constructs the PTP ObjectInfo structure for a RAF upload.
// Must match exactly what the camera expects.
func buildObjectInfo(fileSize int) []byte {
	filename := util.PackPTPString("FUP_FILE.dat")
	empty := []byte{0} // empty PTP string

	buf := make([]byte, 0, 64+len(filename))
	buf = append(buf, util.PackU32(0)...)           // StorageID
	buf = append(buf, util.PackU16(0xF802)...)       // ObjectFormat (RAF)
	buf = append(buf, util.PackU16(0)...)            // ProtectionStatus
	buf = append(buf, util.PackU32(uint32(fileSize))...) // CompressedSize
	buf = append(buf, util.PackU16(0)...)            // ThumbFormat
	buf = append(buf, util.PackU32(0)...)            // ThumbCompressedSize
	buf = append(buf, util.PackU32(0)...)            // ThumbPixWidth
	buf = append(buf, util.PackU32(0)...)            // ThumbPixHeight
	buf = append(buf, util.PackU32(0)...)            // ImagePixWidth
	buf = append(buf, util.PackU32(0)...)            // ImagePixHeight
	buf = append(buf, util.PackU32(0)...)            // ImageBitDepth
	buf = append(buf, util.PackU32(0)...)            // ParentObject
	buf = append(buf, util.PackU16(0)...)            // AssociationType
	buf = append(buf, util.PackU32(0)...)            // AssociationDesc
	buf = append(buf, util.PackU32(0)...)            // SequenceNumber
	buf = append(buf, filename...)                   // Filename
	buf = append(buf, empty...)                      // CaptureDate
	buf = append(buf, empty...)                      // ModificationDate
	buf = append(buf, empty...)                      // Keywords
	return buf
}
