# Vendored Headers

Copy the following from `BOFscale/tailscale/include/` into this directory:

- `asio.hpp`
- `asio/` (directory)
- `jsoncons/` (directory)
- `jsoncons_ext/` (directory)
- `utils.h`

These are vendored copies of:
- **Asio** (standalone, header-only) - async I/O for the C++ BOF-PE modules
- **jsoncons** - JSON parsing/serialization
- **utils.h** - shared utility helpers (djb2a hash, scoped_exit, file I/O)
