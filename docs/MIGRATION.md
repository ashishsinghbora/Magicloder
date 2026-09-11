# Migration from Legacy YouTube Downloader to Magicloder Transfer Engine

## Background

Magicloder originated as a simple script (`YouTube-Downloader.py`) wrapping the `pytube` library to fetch YouTube videos. While functional for basic ad-hoc video extraction, it had several fundamental limitations:
1. **Scope limitation**: It only targeted YouTube streams rather than general HTTP/S, range-supporting CDNs, cloud storage, and generic files.
2. **Fragility**: High-level platform extractors frequently break due to upstream API and signature changes.
3. **Lack of Resumability**: Downloads could not resume from arbitrary byte offsets after network disconnection or process termination.
4. **Single-threaded**: Did not exploit multi-connection range downloading.
5. **No Upload Support**: Provided no bidirectional transfer capabilities (such as Tus resumable uploads).
6. **No Daemon/API Architecture**: Could not be run headlessly on home servers, NAS devices, or controlled via REST/WebSocket/CLI/Tauri UI.

## Transition Plan

1. **Preservation & Deprecation**:
   - `YouTube-Downloader.py` remains in the repository during Phase 1 for historical reference and verification.
   - Dedicated documentation explains the transition from the Python script to the Go engine.
   - The Go engine focuses on the foundational universal primitives: high-performance chunked concurrent HTTP downloading, Tus uploads, robust crash recovery, and rich local APIs.

2. **Target Architecture**:
   - Written in Go with minimal dependencies, utilizing standard library HTTP and concurrency primitives, SQLite for persistence, and `os.File.WriteAt` for deterministic ranged writes.
   - Clean separation between daemon (`magicloder serve`), CLI (`magicloder add`, `magicloder list`), and frontend (Tauri desktop).
