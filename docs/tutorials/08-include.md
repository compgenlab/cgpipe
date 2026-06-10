# Tutorial 8: Composing with include

`include` inlines another `.cgp` file into the current one, in global context.
It's how you share defaults, variables, and whole target libraries across
pipelines — the way you'd keep cluster settings or a common alignment rule in one
place.

## Sharing defaults

Put shared settings in their own file, `defs.cgp`:

```
greeting = "hello from defs"
twice = 2 * 21
```

Then pull them in:

```
#!/usr/bin/env cgp

include "defs.cgp"

print greeting          # hello from defs
print twice             # 42
exit
```

```console
$ cgp pipeline.cgp
hello from defs
42
```

The included file's statements run as if they were typed at the `include` point —
its variables and targets become part of your pipeline. A common use is a
`defaults.cgp` that sets `cgp.runner`, a scheduler account, and `?=` defaults, so
each pipeline starts with `include "defaults.cgp"` and stays free of cluster
specifics.

## Resolution and nesting

`include "path"` resolves **relative to the file doing the including**, then the
current directory. Includes nest: a file you include can include others.

```
# top.cgp
a = "top"
include "mid.cgp"
```
```
# mid.cgp
b = "mid"
include "leaf.cgp"
```
```
# leaf.cgp
c = "leaf"
```

Including `top.cgp` brings in all three:

```console
$ cgp run.cgp           # run.cgp does: include "top.cgp"; print a, b, c
top mid leaf
```

## include vs. snippet

- **`include`** shares **cgp code** — variables, `?=` defaults, target definitions,
  even `@pre`/`@post`. It runs in global context.
- **`snippet`/`@name`** ([Tutorial 7](07-snippets.md)) shares **shell body text**,
  spliced into specific bodies.

Use `include` to assemble a pipeline from reusable parts; use snippets to reuse a
shell fragment inside bodies.

## Next

- **[Tutorial 9: Containerized jobs](09-containers.md)** — run bodies inside Docker
  or Singularity.
- **[Configuration Reference](../14-Configuration_Reference.md)** — what to put in a
  shared `defaults.cgp` vs. `~/.cgp/config`.

Reference → [language-spec.md §5.2](../language-spec.md#52-statement-keywords).
