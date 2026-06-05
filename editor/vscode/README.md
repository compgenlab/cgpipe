# cgp for VSCode

Language support for [cgp](https://github.com/compgen-io/cgp) pipeline scripts
(`.cgp`, `.cgp2`).

## Features

- **Syntax highlighting** for the cgp language: comments, strings with `${…}`
  interpolation, numbers, keywords (`if`/`elif`/`else`/`for`/`in`), booleans,
  built-in statements (`print`, `exit`, `include`, …), `output : input` target
  rules, reserved targets (`@pre`, `@post`, `@default`, …), operators, and
  `{{ … }}` raw shell bodies (highlighted as embedded shell, with `${…}`
  interpolation still recognized inside them).

  Highlighting is provided by a TextMate grammar and works on its own — no
  language server required.

- **Language server** (optional): when the `cgp` binary is on your `PATH`, the
  extension starts `cgp lsp` to add diagnostics (parse errors), semantic
  tokens, hover, and completion. See the `cgp.serverPath` setting to point at a
  specific binary.

## Installing (local)

Build a `.vsix` and install it into VSCode:

```sh
cd editor/vscode
npm install
npx @vscode/vsce package
code --install-extension cgp-*.vsix
```

Then open any `.cgp` / `.cgp2` file.

## Development

Press `F5` in VSCode (with this folder open) to launch an Extension Development
Host with the extension loaded. Use **Developer: Inspect Editor Tokens and
Scopes** to inspect the grammar scopes assigned to a piece of code.
