# Cocoon — G-code Generator for Filament Winding

**Cocoon** stands for **C**nc **O**perated **CO**mposite **O**verwrap **N**avigator.

It turns a wind configuration (mandrel profile, filament, layer stack) into a
tool path and G-code, with a live 3D preview and coverage analysis.

## Architecture

The winding math is pure Go with **no framework dependencies**, shared by every
target:

```
internal/wind/     core: types, mandrel, pathgen, gcode, coverage, JSON parsing
cmd/cocoon/        CLI: cocoon <file.json> -> gcode/<file>.gcode
cmd/wasm/          thin wasm bridge exposing the core to the browser
web/               the GUI: a PWA (HTML/CSS/JS + three.js)
docs/              requirements and design notes
```

The GUI runs in the browser and talks to the Go core compiled to WebAssembly.
The wasm core is ~4.6 MB (1.3 MB gzipped).

## Build

```bash
./build.sh              # CLI (linux + windows) and the web app
./scripts/build-web.sh  # web app only
```

Then serve `web/`:

```bash
python3 -m http.server 8080 --directory web
```

The app is an installable PWA and works offline after first load.

## Using it

- **Blocks** view: build the layer stack visually — pick hoop or helical, set
  parameters, drag to reorder, duplicate or delete. `repeat: 0` mutes a layer.
- **JSON** view: the same config as text, accepting JSON5 (comments, unquoted
  keys, trailing commas). Both views edit one shared model.
- **Coverage** tab: deposited thickness along the mandrel, with bare-sector
  warnings.
- **G-code** tab: the generated program, downloadable.
- Work is autosaved to IndexedDB, so a reload restores where you were.

Saving uses the File System Access API (Chromium only); Firefox and Safari fall
back to downloading a copy.

## CLI

```bash
./cocoon winds/test.json    # writes gcode/test.gcode
```

## Known gaps

See `docs/REQUIREMENTS.md` for the full picture. In short: feedrate and layer
thickness are parsed but not applied to output, progress markers are positional
rather than time-based, and `StartGcode`/`EndGcode` are reserved but unused.
