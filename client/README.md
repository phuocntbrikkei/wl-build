# README

## About

This is the official Wails Vanilla template.

You can configure the project by editing `wails.json`. More information about the project settings can be found
here: https://wails.io/docs/reference/project-config

## Live Development

To run in live development mode, run `wails dev` in the project directory. This will run a Vite development
server that will provide very fast hot reload of your frontend changes. If you want to develop in a browser
and have access to your Go methods, there is also a dev server that runs on http://localhost:34115. Connect
to this in your browser, and you can call your Go code from devtools.

## Building

### Windows
To build for Windows:
- Run `wails build` (or `wails build -platform windows/amd64`)
- Or use the PowerShell build script: `./build.ps1`

### macOS (ARM & Intel)
To build for macOS:
- To build Apple Silicon (ARM64) target: `wails build -platform darwin/arm64`
- To build Intel (AMD64) target: `wails build -platform darwin/amd64`
- To build a Universal macOS binary: `wails build -platform darwin/universal`
- Alternatively, run the helper script to build both ARM64 and AMD64 targets:
  ```bash
  chmod +x build.sh
  ./build.sh
  ```
