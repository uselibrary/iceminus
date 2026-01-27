# iceminus

A Go project created with gocar.

## Build

```bash
# Debug build (current platform)
gocar build

# Release build (current platform)
gocar build --release

# Cross-compile for Linux on AMD64
gocar build --target linux/amd64
```

## Run

```bash
gocar run
```

## Output Structure

```
bin/
├── debug/
│   └── <os>-<arch>/
│       └── iceminus
└── release/
    └── <os>-<arch>/
        └── iceminus
```

Build artifacts are organized by:
- **Build mode**: debug or release
- **Target platform**: OS and architecture (e.g., linux-amd64, darwin-arm64)

Examples:
- Debug build for current platform: `./bin/debug/linux-amd64/iceminus`
- Release build for Windows: `./bin/release/windows-amd64/iceminus.exe`
