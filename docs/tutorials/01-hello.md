# Tutorial 1: Hello, target

The smallest useful pipeline: one target, a command-line variable, and a help
block. By the end you will have written a `.cgp` file, rendered it, run it, and
seen cgp skip work that is already done.

## The script

Create `hello.cgp`:

```
#!/usr/bin/env cgp
#
# Greet someone.
#
# Options:
#     --name NAME   who to greet (default: world)

name ?= "world"

hello.txt: {{
    echo "Hello, ${name}!" > ${output}
}}

@default: hello.txt
```

Line by line:

- `#!/usr/bin/env cgp` — the shebang. With it (and `chmod +x`) you can run the
  file directly as `./hello.cgp`.
- The comment block under the shebang is the **help text**. The first blank or
  non-comment line ends it.
- `name ?= "world"` — `?=` assigns *only if `name` is not already set*. So a
  `--name` on the command line wins, and otherwise it defaults to `world`.
- `hello.txt: {{ ... }}` — a target that produces `hello.txt`. It has **no
  inputs** (nothing before `{{` after the colon), so it only depends on its own
  existence. `${output}` inside the body is the declared output, `hello.txt`.
- `@default: hello.txt` — build this when no target is named on the command line.

## Render it

The default runner assembles a bash script and prints it (it does not run it):

```console
$ cgp hello.cgp
#!/usr/bin/env bash
set -euo pipefail

# ---- hello.txt ----
echo "Hello, world!" > hello.txt
```

Pass `--name` to override the default:

```console
$ cgp hello.cgp --name Marcus
#!/usr/bin/env bash
set -euo pipefail

# ---- hello.txt ----
echo "Hello, Marcus!" > hello.txt
```

## Run it

Pipe the rendered script to a shell:

```console
$ cgp hello.cgp --name Marcus | bash
$ cat hello.txt
Hello, Marcus!
```

## cgp skips work that is already done

Run the same command again. Because `hello.txt` already exists and nothing it
depends on has changed, cgp emits **no work** — just the empty bash preamble:

```console
$ cgp hello.cgp --name Marcus
#!/usr/bin/env bash
set -euo pipefail
```

To rebuild regardless, force it:

```console
$ cgp -force hello.cgp --name Marcus
#!/usr/bin/env bash
set -euo pipefail

# ---- hello.txt ----
echo "Hello, Marcus!" > hello.txt
```

This is the heart of cgp: you describe outputs, and it only does the work that is
actually needed. (For real inputs, "needed" means *the output is missing or older
than an input* — see [The Ledger](../10-The_Ledger.md) for the full staleness
story.)

## See the help text

```console
$ cgp hello.cgp -h
Greet someone.

Options:
    --name NAME   who to greet (default: world)
```

The help block you wrote at the top is what `-h` prints (when it comes *after* the
filename — `cgp -h` alone prints cgp's own usage).

## Next

- **[Tutorial 2: gzip with a wildcard](02-gzip-wildcard.md)** — one rule that
  matches many files.
- **[Build Targets](../05-Build_Targets.md)** — inputs, directives, and the rest of
  what a target can do.

Reference → [language-spec.md §6](../language-spec.md#6-target-bodies),
[§8](../language-spec.md#8-reserved-targets--prefixed).
