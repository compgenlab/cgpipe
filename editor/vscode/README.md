# cgpipe for VSCode

Language support for [cgpipe](https://github.com/compgenlab/cgpipe) pipeline scripts
(`.cgp`, `.cgp2`).

## Features

- **Syntax highlighting** for the cgpipe language: comments, strings with `${…}`
  interpolation, numbers, keywords (`if`/`elif`/`else`/`for`/`in`), booleans,
  built-in statements (`print`, `exit`, `include`, …), `output : input` target
  rules, reserved targets (`@pre`, `@post`, `@default`, …), operators, and
  `{{ … }}` raw shell bodies (highlighted as embedded shell, with `${…}`
  interpolation still recognized inside them).

  Highlighting is provided by a TextMate grammar and works on its own — no
  language server required.

- **Language server** (optional): when the `cgpipe` binary is on your `PATH`, the
  extension starts `cgp lsp` to add diagnostics (parse errors), semantic
  tokens, hover, and completion. See the `cgpipe.serverPath` setting to point at a
  specific binary.

## Installing (local)

Build a `.vsix` and install it into VSCode:

```sh
cd editor/vscode
npm install
npx @vscode/vsce package
code --install-extension cgpipe-*.vsix
```

Then open any `.cgp` / `.cgp2` file.

## Development

Press `F5` in VSCode (with this folder open) to launch an Extension Development
Host with the extension loaded. Use **Developer: Inspect Editor Tokens and
Scopes** to inspect the grammar scopes assigned to a piece of code.
