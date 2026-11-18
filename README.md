# BOFbird - NetBird as a BOF-PE

BOFbird packages the [NetBird](https://netbird.io/) mesh networking client as a set of **BOF-PE** (Beacon Object File - Portable Executable) modules. When loaded by a C2 framework the modules run entirely in-process; when executed standalone they use a compatibility `beacon.dll` that prints to stdout.

The architecture mirrors [BOFscale](https://github.com/netspi/BOFscale) (Tailscale BOF-PE) but targets NetBird's `client/embed` package for userspace (netstack) networking with in-memory state.

## Components

| Module | Language | Type | Description |
|--------|----------|------|-------------|
| **netbirdd** | Go | Async BOF-PE | NetBird daemon - embeds `client/embed.Client`, exposes HTTP/1.0 LocalAPI on a Windows named pipe, optional SOCKS5 proxy through the NetBird overlay |
| **netbird** | C++ | BOF-PE | CLI client - talks to the daemon's LocalAPI over the named pipe (`status`, `up`, `down`, `shutdown`) |
| **portfwd** | C++ | Async BOF-PE | TCP port forwarder - accepts local connections and forwards them through the daemon's SOCKS5 proxy to a target on the NetBird network |
| **loader** | C++ | Executable | BOF-PE reflective loader - maps a BOF-PE `.o` into memory, patches beacon.dll imports, resolves exports, adds exception support, and calls the entry point |
| **beacon** | C++ | Shared lib | Cobalt-Strike-compatible Beacon API shim for standalone execution |

## Quick Start

### 1. Deploy the NetBird Server

```bash
cd netbird-server
NETBIRD_DOMAIN=d1a2b3c4e5f6g7.cloudfront.net docker compose up
```

### 2. Start the Daemon (async BOF, runs in background)

Via C2:
```
netbirdd --setup-key <KEY> --management-url https://mgmt.example.com --socks5-server localhost:1080
```

Note the socket path from the output (e.g. `\\.\pipe\<UUID>`).

Via standalone loader (`loader.exe` + `beacon.dll` must be in the same directory):
```
loader.exe netbirdd.x64.o zzzzzz --setup-key <KEY> --management-url https://mgmt.example.com --socks5-server localhost:1080
```

### 3. Control the Daemon via the CLI

Via C2:
```
netbird --socket \\.\pipe\<UUID> status
netbird --socket \\.\pipe\<UUID> shutdown
```

Via standalone loader:
```
loader.exe netbird.x64.o zzz status --socket \\.\pipe\<UUID>
loader.exe netbird.x64.o zzzz status --socket \\.\pipe\<UUID> --json
loader.exe netbird.x64.o zzz shutdown --socket \\.\pipe\<UUID>
```

### 4. Forward a Local Port to a Peer

Via C2:
```
portfwd --t 100.64.0.5 --tp 3389 --p 3389 --sp 1080
```

Via standalone loader:
```
loader.exe portfwd.x64.o zzzzzzzz --t 100.64.0.5 --tp 3389 --p 3389 --sp 1080
```

## Argument Format String

The standalone loader requires an **argument format string** between the `.o` file path and the actual arguments. Each character in the format string tells the packer how to serialize the corresponding CLI argument:

| Char | Type | Description |
|------|------|-------------|
| `z` | string | Null-terminated UTF-8 string |
| `Z` | wstring | Null-terminated UTF-16 wide string |
| `s` | short | 16-bit integer |
| `i` | int | 32-bit integer |
| `b` | binary | File path - contents read and packed as raw bytes |

**The format string must be at least as long as the number of CLI arguments** (it may be longer; extra characters are ignored). `status --socket \\.\pipe\abc` is 3 tokens so the format is `zzz`. When run through a C2 framework the framework handles argument packing internally and the format string is not needed.

**Gotcha with `z`:** if an argument happens to be an existing file path, the packer reads the file's **contents** instead of packing the path string. This is intentional (so you can pass file data inline) but means a real path is never transmitted literally. Named pipe paths beginning with `\\.\pipe\` are special-cased and always packed as strings. Use `Z` for an unconditional wide string.

## Build Output Tree

```
dist/
+-- pe/
|   +-- netbirdd.x64.o       # Go daemon BOF-PE
|   +-- netbird.x64.o        # C++ CLI BOF-PE
|   +-- portfwd.x64.o        # C++ port forwarder BOF-PE
+-- x64/
|   +-- loader.exe            # BOF-PE reflective loader
|   +-- netbirdd.exe          # Standalone daemon (no loader needed)
|   +-- beacon.dll            # Beacon compatibility DLL
+-- ost/
    +-- netbirdd_ost_bof.s1.py
    +-- netbird_ost_bof.s1.py
    +-- portfwd_ost_bof.s1.py
```

## Build Instructions

### Prerequisites

- CMake >= 3.18
- MSVC (Visual Studio 2022), Clang-CL, or MinGW-w64 cross-compiler
- Go >= 1.24 (only for the Go daemon targets)

### Clone

```bash
git clone --recursive https://github.com/YourOrg/BOFbird.git
cd BOFbird
```

The `--recursive` flag pulls the NetBird submodule at `netbird/netbirdd/netbird/`.

### Windows (MSVC, recommended)

Produces BOF-PEs with proper `/MERGE:` section merging and `/SAFESEH` support.

```cmd
cd netbird
cmake -B build -G "Visual Studio 17 2022" -A x64
cmake --build build --config Release
cmake --install build --config Release
```

### WSL / Linux Cross-Compile (MinGW)

```bash
cd netbird
cmake -B build -DCMAKE_BUILD_TYPE=MinSizeRel \
  -DCMAKE_C_COMPILER=x86_64-w64-mingw32-gcc \
  -DCMAKE_CXX_COMPILER=x86_64-w64-mingw32-g++ \
  -DCMAKE_SYSTEM_NAME=Windows \
  -DBUILD_GO_TARGETS=OFF
cmake --build build -j$(nproc)
cmake --install build
```

Use `-DBUILD_GO_TARGETS=OFF` to skip the Go daemon if Go is not installed.

### Linux Cross-Compile (MSVC via container)

Using the `ccob/windows-llvm-cross-msvc` container image (same as CI):

```bash
# x64
cmake -B build-x64 -DCMAKE_BUILD_TYPE=MinSizeRel \
  -DCMAKE_TOOLCHAIN_FILE=/opt/toolchain/x86_64-pc-windows-msvc.cmake
cmake --build build-x64 -j$(nproc)
cmake --install build-x64

# x86
cmake -B build-x86 -DCMAKE_BUILD_TYPE=MinSizeRel \
  -DCMAKE_TOOLCHAIN_FILE=/opt/toolchain/i386-pc-windows-msvc.cmake
cmake --build build-x86 -j$(nproc)
cmake --install build-x86
```

### Building the Go Daemon

The Go daemon requires `go mod tidy` before building:

```bash
cd netbird/netbirdd
go mod tidy
cd ..
cmake -B build ... -DBUILD_GO_TARGETS=ON
cmake --build build -j$(nproc)
```

## CI

The GitHub Actions workflow (`.github/workflows/build.yml`) cross-compiles both x64 and x86 on push to `main` and uploads the combined `bofbird-dist` artifact.

## Self-Hosted NetBird Server

See [`netbird-server/`](netbird-server/) for a Docker Compose deployment of a self-hosted NetBird management + signal + relay stack, designed to sit behind a CDN for domain fronting.

## License

BOFbird's own code is BSD 2-Clause. See [LICENSE](LICENSE).

BOFbird links against and incorporates code from several third-party projects,
each under its own license. See [THIRD-PARTY-NOTICES](THIRD-PARTY-NOTICES) for
full attribution and license texts.

| Component | License | Notes |
|---|---|---|
| NetBird client libraries (`client/embed`, etc.) | BSD 3-Clause | Linked at build time via Go module |
| NetBird server (`management/`, `signal/`, `relay/`) | AGPLv3 | **Not incorporated** - `netbird-server/` runs official container images only |
| BOF-PE / BOFscale (beacon compat, linker scripts, loader) | BSD 3-Clause | Copied from NetSPI's reference design |
| formatPE | MIT | Modified PE parser used by the loader |
| Asio | Boost Software License 1.0 | Header-only, vendored |
| jsoncons | Boost Software License 1.0 | Header-only, vendored |
| go-winio | MIT | Go dependency |
