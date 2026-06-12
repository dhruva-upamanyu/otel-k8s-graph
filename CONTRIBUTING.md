# Contributing

Thanks for your interest in contributing!

## Building and testing

Requires Go 1.25+. No cluster or Redis needed — tests use
[miniredis](https://github.com/alicebob/miniredis) and client-go's fake
clientset.

```bash
go build ./...
go test ./...
go test -race ./internal/graph/ ./internal/k8swatch/
go vet ./...
```

For chart changes:

```bash
helm lint helm/graph --set image.registry=example.test/repo
helm template graph helm/graph --set image.registry=example.test/repo
```

If you change `helm/graph/values.yaml` comments or `README.md.gotmpl`,
regenerate the chart README with [helm-docs](https://github.com/norwoodj/helm-docs):

```bash
helm-docs --chart-search-root helm
```

## Submitting changes

1. Fork and create a feature branch.
2. Keep commits focused; explain the why in the commit message.
3. Make sure `go build`, `go test`, `go vet`, and `helm lint` pass.
4. Open a pull request describing the change and any trade-offs.

## License

By contributing, you agree that your contributions are licensed under the
Apache License 2.0 (see [LICENSE](LICENSE)).
