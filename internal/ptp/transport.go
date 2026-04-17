// USB transport layer for PTP communication using libusb (via gousb).
// Port of filmkit/src/ptp/transport.ts — replaces WebUSB with libusb.
package ptp

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/google/gousb"
)

const chunkSize = 512 * 1024 // 512 KB — matches filmkit's chunking
const defaultTimeout = 5 * time.Second

// Transport handles raw USB PTP communication.
type Transport struct {
	usbCtx *gousb.Context
	dev    *gousb.Device
	cfg    *gousb.Config
	intf   *gousb.Interface
	epOut  *gousb.OutEndpoint
	epIn   *gousb.InEndpoint
	txID   uint32
	log    func(string)
}

// NewTransport creates a new Transport with an optional log function.
func NewTransport(logFn func(string)) *Transport {
	if logFn == nil {
		logFn = func(s string) { log.Println(s) }
	}
	return &Transport{log: logFn}
}

// Connected returns true if a USB device is open.
func (t *Transport) Connected() bool {
	return t.dev != nil
}

// ProductName returns the USB product name string from the device descriptor.
func (t *Transport) ProductName() string {
	if t.dev == nil {
		return ""
	}
	name, _ := t.dev.Product()
	return name
}

// Connect finds and opens a Fujifilm camera via libusb.
// Automatically detaches any kernel driver holding the device.
func (t *Transport) Connect() error {
	ctx := gousb.NewContext()

	var foundDev *gousb.Device
	devs, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		if uint16(desc.Vendor) != FujiVendorID {
			return false
		}
		for _, pid := range FujiProductIDs {
			if uint16(desc.Product) == pid {
				return true
			}
		}
		return false
	})
	if err != nil && len(devs) == 0 {
		ctx.Close()
		return fmt.Errorf("USB device search failed: %w", err)
	}
	if len(devs) == 0 {
		ctx.Close()
		return fmt.Errorf("no Fujifilm camera found on USB")
	}
	foundDev = devs[0]
	for _, d := range devs[1:] {
		d.Close()
	}

	t.usbCtx = ctx
	t.dev = foundDev

	// Auto-detach kernel driver (e.g. usb-storage, gphoto2 in background)
	t.dev.SetAutoDetach(true)

	cfg, err := t.dev.Config(1)
	if err != nil {
		t.Close()
		return fmt.Errorf("USB config(1) failed: %w", err)
	}
	t.cfg = cfg

	intf, err := cfg.Interface(0, 0)
	if err != nil {
		t.Close()
		return fmt.Errorf("USB interface(0,0) failed: %w", err)
	}
	t.intf = intf

	// Find bulk IN and OUT endpoints
	for _, ep := range intf.Setting.Endpoints {
		if ep.TransferType != gousb.TransferTypeBulk {
			continue
		}
		switch ep.Direction {
		case gousb.EndpointDirectionOut:
			t.epOut, err = intf.OutEndpoint(ep.Number)
			if err != nil {
				t.Close()
				return fmt.Errorf("OUT endpoint failed: %w", err)
			}
		case gousb.EndpointDirectionIn:
			t.epIn, err = intf.InEndpoint(ep.Number)
			if err != nil {
				t.Close()
				return fmt.Errorf("IN endpoint failed: %w", err)
			}
		}
	}

	if t.epOut == nil || t.epIn == nil {
		t.Close()
		return fmt.Errorf("could not find bulk endpoints on interface 0")
	}

	t.txID = 0
	t.log(fmt.Sprintf("USB connected: PID=0x%04X", uint16(foundDev.Desc.Product)))
	if t.epOut != nil {
		t.log(fmt.Sprintf("USB OUT endpoint: maxPktSize=%d (512=HS, 64=FS)", t.epOut.Desc.MaxPacketSize))
	}
	if t.epIn != nil {
		t.log(fmt.Sprintf("USB IN endpoint: maxPktSize=%d", t.epIn.Desc.MaxPacketSize))
	}
	return nil
}

// Close releases the USB interface and closes the device.
func (t *Transport) Close() {
	t.epOut = nil
	t.epIn = nil
	if t.intf != nil {
		t.intf.Close()
		t.intf = nil
	}
	if t.cfg != nil {
		t.cfg.Close()
		t.cfg = nil
	}
	if t.dev != nil {
		t.dev.Close()
		t.dev = nil
	}
	if t.usbCtx != nil {
		t.usbCtx.Close()
		t.usbCtx = nil
	}
}

// Reset releases and re-opens the USB interface, resetting transaction IDs.
// Used to recover from stale PTP session state.
func (t *Transport) Reset() error {
	if t.intf != nil {
		t.intf.Close()
		t.intf = nil
	}
	t.epOut = nil
	t.epIn = nil
	t.txID = 0

	intf, err := t.cfg.Interface(0, 0)
	if err != nil {
		return fmt.Errorf("USB reset interface failed: %w", err)
	}
	t.intf = intf

	var setErr error
	for _, ep := range intf.Setting.Endpoints {
		if ep.TransferType != gousb.TransferTypeBulk {
			continue
		}
		switch ep.Direction {
		case gousb.EndpointDirectionOut:
			t.epOut, setErr = intf.OutEndpoint(ep.Number)
		case gousb.EndpointDirectionIn:
			t.epIn, setErr = intf.InEndpoint(ep.Number)
		}
		if setErr != nil {
			return fmt.Errorf("USB reset endpoint failed: %w", setErr)
		}
	}

	t.log("USB connection reset")
	return nil
}

func (t *Transport) nextTxID() uint32 {
	t.txID++
	return t.txID
}

// send writes a Container to the camera, chunking at 512 KB.
func (t *Transport) send(c Container) error {
	data := Pack(c)
	for offset := 0; offset < len(data); {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]
		n, err := t.epOut.Write(chunk)
		if err != nil {
			return fmt.Errorf("USB write failed: %w", err)
		}
		if n != len(chunk) {
			return fmt.Errorf("USB write incomplete: wrote %d of %d bytes", n, len(chunk))
		}
		offset += n
	}
	return nil
}

// recv reads a Container from the camera, reassembling multi-packet responses.
func (t *Transport) recv(timeout time.Duration) (Container, error) {
	buf := make([]byte, chunkSize)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	n, err := t.epIn.ReadContext(ctx, buf)
	if err != nil {
		return Container{}, fmt.Errorf("USB read failed: %w", err)
	}
	data := make([]byte, n)
	copy(data, buf[:n])

	// Reassemble if response is split across multiple USB packets
	totalLength := Length(data)
	for uint32(len(data)) < totalLength {
		if len(data) > 100*1024*1024 {
			return Container{}, fmt.Errorf("response too large: %d bytes", len(data))
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), timeout)
		n, err = t.epIn.ReadContext(ctx2, buf)
		cancel2()
		if err != nil {
			return Container{}, fmt.Errorf("USB read continuation failed: %w", err)
		}
		data = append(data, buf[:n]...)
	}

	return Unpack(data)
}

// SendCommand sends a PTP operation and returns the response.
// If the camera sends a DATA phase before the RESPONSE, the data is returned.
func (t *Transport) SendCommand(opcode uint16, params []uint32, timeout time.Duration) (code uint16, respParams []uint32, data []byte, err error) {
	if timeout == 0 {
		timeout = defaultTimeout
	}
	tid := t.nextTxID()

	err = t.send(Container{
		Type: ContainerCommand, Code: opcode,
		TransactionID: tid, Params: params,
	})
	if err != nil {
		return
	}

	resp, err := t.recv(timeout)
	if err != nil {
		return
	}

	if resp.Type == ContainerData {
		data = resp.Data
		resp, err = t.recv(timeout)
		if err != nil {
			return
		}
	}

	if resp.Type != ContainerResponse {
		err = fmt.Errorf("expected RESPONSE container, got type 0x%04X", resp.Type)
		return
	}

	code = resp.Code
	respParams = resp.Params
	return
}

// SendDataCommandFromReader sends a PTP command with a streaming data payload.
// Unlike SendDataCommand, it never buffers the full payload — it streams from r
// in chunks directly to the USB endpoint. Use this for large payloads (e.g. RAF files)
// to avoid holding the entire file in RAM.
func (t *Transport) SendDataCommandFromReader(opcode uint16, params []uint32, payloadSize int64, r io.Reader, timeout time.Duration) (code uint16, respParams []uint32, err error) {
	if timeout == 0 {
		timeout = defaultTimeout
	}
	tid := t.nextTxID()

	// COMMAND phase
	err = t.send(Container{
		Type: ContainerCommand, Code: opcode,
		TransactionID: tid, Params: params,
	})
	if err != nil {
		return
	}

	// DATA phase — stream exactly chunkSize bytes per USB write (matching send()).
	// The original send() slices a flat buffer in chunkSize pieces, so every write
	// except the last is exactly chunkSize = 512 KB with no short packets between
	// chunks.  Short packets (< 512 bytes USB packet) signal end-of-transfer to the
	// camera; sending one after every chunk confuses its receive logic and causes
	// a stall after ~3 MB.
	//
	// Layout: [12-byte PTP header | payload bytes] — header is the first 12 bytes
	// of the stream, payload fills the rest, chunked at chunkSize boundaries.
	totalLen := uint32(headerSize) + uint32(payloadSize)
	header := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(header[0:4], totalLen)
	binary.LittleEndian.PutUint16(header[4:6], ContainerData)
	binary.LittleEndian.PutUint16(header[6:8], opcode)
	binary.LittleEndian.PutUint32(header[8:12], tid)

	// Build a virtual stream: header bytes followed by payload from r.
	// io.MultiReader lets us read header+payload as one continuous byte stream.
	combined := io.MultiReader(
		bytes.NewReader(header),
		io.LimitReader(r, payloadSize),
	)

	buf := make([]byte, chunkSize) // exactly chunkSize per write
	var written int64
	start := time.Now()
	logInterval := int64(10 * 1024 * 1024)
	nextLog := logInterval

	for {
		n, readErr := io.ReadFull(combined, buf)
		if n > 0 {
			t.log(fmt.Sprintf("RAF stream: writing %d bytes to USB (payload=%d)", n, written))
			wStart := time.Now()
			// 10-minute timeout per chunk: the camera can pause ~54s after its 3MB
			// USB buffer fills while it initialises the SD card write path.
			wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Minute)
			_, writeErr := t.epOut.WriteContext(wctx, buf[:n])
			wcancel()
			wElapsed := time.Since(wStart)
			if writeErr != nil {
				err = fmt.Errorf("USB stream failed after %d MB (%v): %w", written>>20, wElapsed, writeErr)
				return
			}
			t.log(fmt.Sprintf("RAF stream: done in %v", wElapsed))
			// written tracks payload bytes only (subtract header from first chunk)
			if written == 0 && n >= headerSize {
				written += int64(n - headerSize)
			} else {
				written += int64(n)
			}
			if written >= nextLog {
				elapsed := time.Since(start).Seconds()
				mbps := float64(written) / (1024 * 1024) / elapsed
				t.log(fmt.Sprintf("RAF upload: %d/%d MB (%.1f MB/s)", written>>20, payloadSize>>20, mbps))
				nextLog += logInterval
			}
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			err = readErr
			return
		}
	}

	// RESPONSE phase
	resp, err := t.recv(timeout)
	if err != nil {
		return
	}
	if resp.Type != ContainerResponse {
		err = fmt.Errorf("expected RESPONSE container, got type 0x%04X", resp.Type)
		return
	}

	code = resp.Code
	respParams = resp.Params
	return
}

// SendDataCommand sends a PTP command with a data payload (COMMAND + DATA phases).
// Used for SetDevicePropValue, SendObjectInfo, SendObject, etc.
func (t *Transport) SendDataCommand(opcode uint16, params []uint32, payload []byte, timeout time.Duration) (code uint16, respParams []uint32, err error) {
	if timeout == 0 {
		timeout = defaultTimeout
	}
	tid := t.nextTxID()

	t.log(fmt.Sprintf("SendDataCommand 0x%04X: sending COMMAND (tid=%d, payload=%d B)", opcode, tid, len(payload)))
	err = t.send(Container{
		Type: ContainerCommand, Code: opcode,
		TransactionID: tid, Params: params,
	})
	if err != nil {
		return
	}

	t.log(fmt.Sprintf("SendDataCommand 0x%04X: sending DATA (%d B)...", opcode, len(payload)))
	err = t.send(Container{
		Type: ContainerData, Code: opcode,
		TransactionID: tid, Data: payload,
	})
	if err != nil {
		return
	}
	t.log(fmt.Sprintf("SendDataCommand 0x%04X: DATA sent, waiting RESPONSE (timeout=%v)...", opcode, timeout))

	resp, err := t.recv(timeout)
	if err != nil {
		t.log(fmt.Sprintf("SendDataCommand 0x%04X: recv error: %v", opcode, err))
		return
	}
	t.log(fmt.Sprintf("SendDataCommand 0x%04X: got response type=0x%04X code=0x%04X", opcode, resp.Type, resp.Code))

	if resp.Type != ContainerResponse {
		err = fmt.Errorf("expected RESPONSE container, got type 0x%04X", resp.Type)
		return
	}

	code = resp.Code
	respParams = resp.Params
	return
}
