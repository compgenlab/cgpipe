# Tutorial 7: Importable snippets

`@pre`/`@post` wrap *every* job. Sometimes you instead want a reusable chunk of
shell that you splice into *specific* bodies — a strict-mode preamble, a
module-load block, a helper function. That's a **snippet**.

## Define and use

`build.cgp`:

```
#!/usr/bin/env cgp

snippet common {{
    set -euo pipefail
    umask 077
}}

out.txt: in.txt {{
    @common
    wc -l ${input} > ${output}
}}
@default: out.txt
```

A `snippet name {{ }}` defines a named fragment; `@name` inside a body splices its
lines in at that point.

## Render it

```console
$ cgp -dr build.cgp
#!/usr/bin/env bash
set -euo pipefail

# ---- out.txt ----
set -euo pipefail
umask 077
wc -l in.txt > out.txt
```

`@common` was replaced by the snippet's two lines, right where it appeared. You can
use `@common` in as many bodies as you like, and put `@name` anywhere in a body —
beginning, middle, or end.

## Snippet vs. include vs. @pre

Three ways to avoid repeating yourself, for three different scopes:

| Tool | Shares | Applies to |
|------|--------|------------|
| `snippet` / `@name` | a **body fragment** (shell) | the bodies you splice it into |
| `@pre` / `@post` | a **body wrapper** (shell) | *every* target automatically |
| `include` | **targets and variables** (cgp) | the whole pipeline |

Reach for a snippet when several jobs need the same shell lines but not *all* jobs
do, and you'd rather name the fragment than opt individual jobs out of an `@pre`.

## Next

- **[Tutorial 8: Composing with include](08-include.md)** — share whole targets and
  defaults across files.

Reference → [Build Targets § Snippets](../05-Build_Targets.md#snippets),
[language-spec.md §6.6](../language-spec.md#66-snippets).
