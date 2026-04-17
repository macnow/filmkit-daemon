# filmkit-daemon

USB-to-HTTP bridge for Fujifilm cameras on GL.inet routers.

Runs as a background service on the router and exposes a REST API that talks to the camera via PTP/USB. The [FilmKit](https://github.com/macnow/filmkit) web app connects to this API, enabling full preset editing and RAW conversion from any browser — including iOS/iPadOS where WebUSB is unavailable.

Part of the [filmkit-glinet](https://github.com/macnow/filmkit-glinet) integration.

## Tested hardware

| Router | Architecture |
|--------|-------------|
| GL.inet GL-BE9300 | aarch64_cortex-a53 |
| GL.inet GL-E5800 | aarch64 |

Camera support follows [eggricesoy/filmkit](https://github.com/eggricesoy/filmkit) — currently tested on **X100VI**.

## Architecture

```
Browser (FilmKit web app)
        │  HTTP/JSON  (Wi-Fi)
        ▼
filmkit-daemon  (GL.inet router, port 8765)
        │  PTP/USB
        ▼
Fujifilm camera
```

The daemon:
- Serves the FilmKit frontend as static files (`/www/filmkit`)
- Exposes a REST API on `:8765`
- Serialises all camera operations behind a mutex (single PTP session)

## API endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET`  | `/api/status` | Liveness check; returns `{"connected": bool, ...}` |
| `POST` | `/api/connect` | Open PTP session; returns model name and mode flags |
| `POST` | `/api/disconnect` | Close PTP session |
| `GET`  | `/api/presets` | Read all 7 preset slots from camera |
| `PUT`  | `/api/presets/:slot` | Write preset to camera slot |
| `POST` | `/api/raf/load` | Upload RAF file (binary body); returns JPEG preview |
| `GET`  | `/api/raf/profile` | Return cached base D185 profile bytes |
| `POST` | `/api/raf/reconvert-raw` | Reconvert with modified profile (binary body) |
| `GET`  | `/api/files` | List RAF files on camera SD card |
| `GET`  | `/api/files/:handle` | Download RAF file by PTP object handle |
| `GET`  | `/api/files/:handle/thumb` | JPEG thumbnail for a file |

### Camera mode flags (returned by `/api/connect`)

```json
{ "model": "X100VI", "rawConversion": true, "rafLoaded": false }
```

- `rawConversion: true` — camera is in **USB Raw Conv./Backup Restore** mode: preset editing and RAF conversion available.
- `rawConversion: false` — camera is in standard PTP mode: file browsing available, preset editing not available. Switch on camera: *Network/USB Setting → Connection Mode → USB Raw Conv./Backup Restore*.

## Building

### Prerequisites

- Go 1.21+
- Cross-compiler for `aarch64-unknown-linux-musl` (for router builds)
- Static `libusb-1.0` compiled for `arm64-linux-musl`

Install cross-compiler on macOS:
```sh
make install-cross
```

Build static libusb (needed once):
```sh
# Download libusb source and cross-compile
# See: https://libusb.info
# Output must end up in /tmp/libusb-arm64/{include,lib}
```

### Build for router
```sh
make build
# → dist/filmkit-daemon  (arm64 Linux, statically linked)
```

### Build for local testing
```sh
make build-local
# → dist/filmkit-daemon-local
```

## Deployment

### Quick deploy (daemon only)
```sh
make deploy ROUTER_IP=10.0.1.1
```

Uploads the binary to `/usr/bin/filmkit-daemon` and the init script to `/etc/init.d/filmkit`, then restarts the service.

### Full deploy (daemon + frontend)
Use the [filmkit-glinet](https://github.com/macnow/filmkit-glinet) integration repo which builds both and deploys in one command.

## Running

The daemon is managed by OpenWrt's `procd`:
```sh
/etc/init.d/filmkit enable   # auto-start on boot
/etc/init.d/filmkit start
/etc/init.d/filmkit stop
/etc/init.d/filmkit restart
```

Command-line flags:
```
-port     int     HTTP listen port (default 8765)
-frontend string  Path to built FilmKit frontend (default: <binary_dir>/filmkit)
```

## Project structure

```
cmd/filmkit-daemon/     main entrypoint (flag parsing, server startup)
internal/
  api/                  HTTP server + all route handlers
  ptp/                  PTP/USB transport (session, container, constants)
  profile/              D185 profile patching (mirrors filmkit TypeScript logic)
  util/                 Binary encoding helpers
openwrt/
  files/etc/init.d/     procd init script for OpenWrt
```

## License

MIT — same as [eggricesoy/filmkit](https://github.com/eggricesoy/filmkit).
