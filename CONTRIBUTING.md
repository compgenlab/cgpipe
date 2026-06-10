# Contributing to cgp

cgp is in early development. Contributions are welcome, but the language is
still settling — open an issue to discuss anything substantial before sending a
large PR.

## Development

```sh
go build ./...
go test ./...
```

Before pushing, make sure the code is formatted and vets clean:

```sh
gofmt -l .      # should print nothing
go vet ./...
```

CI runs `build` + `test` on Linux (amd64, arm64) and macOS (amd64, arm64), plus
a `gofmt`/`go vet` lint job. All must pass.

## Conventions

- The language is defined in [`docs/language-spec.md`](docs/language-spec.md).
  Behavior changes should update the spec in the same PR.
- Standard library only — the module has no external dependencies. Keep it that
  way unless there's a compelling reason.
- Keep the parser hand-rolled and the error messages good.

## License

By contributing you agree that your contributions are licensed under the MIT
License (see [LICENSE](LICENSE)).
