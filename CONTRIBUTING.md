# Contributing

**English** · [Français](CONTRIBUTING.fr.md)

Thanks for your interest in frigate-telegram-enhanced! Bug reports, ideas, translations
and pull requests are all welcome.

## Reporting a bug or asking for a feature

Open an [issue](https://github.com/Limoniak/frigate-telegram-enhanced/issues/new/choose)
and pick the matching form. For a bug, the version of the service, the version of
Frigate and the mode (`reviews` or `events`) help a lot; the service's logs
(`docker logs frigate-telegram-enhanced`) even more. Remove your bot token, passwords
and addresses before posting them.

A security issue is not reported in a public issue: see [SECURITY.md](SECURITY.md).

## Translating

A language is one file, `internal/i18n/locales/<code>.json`: see
[Translating](README.md#translating). You can open a pull request with the file, or
attach it to a *Translation* issue if you are not used to Git.

## Changing the code

You need Go (the version of `go.mod`) and, for the web interface tests, Chrome.

```bash
go vet ./...
go test ./...
CHROME_PATH=/path/to/chrome go test -tags browser -run Browser ./internal/web/
go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

The CI runs the same checks on every pull request.

The new interface, the setup and login pages are built from `web/` (Svelte and Tailwind).
`go build` and `go test` need no Node: the build output, `internal/web/dist/`, is committed.
After a change in `web/`:

```bash
cd web
npm ci
npm test
npm run build      # updates internal/web/dist/, commit it with your change
npm run dev        # live reload, with the API of a service running on port 8431
```

`npm run dev` serves the setup page at <http://localhost:5173/setup.html> and the new
interface at <http://localhost:5173/app.html>.

The CI fails when `internal/web/dist/` does not match `web/`. Dependabot's npm pull
requests do not rebuild `dist/`: when the check fails on one, a maintainer runs
`npm run build` on its branch and pushes the result.

Conventions:

- **English** in the code, comments, tests and commit messages. Texts shown to users go
  through the catalogs: `T("…")` in Go and JavaScript, `data-t="…"` in HTML. Every
  catalog must translate every text: `go test ./internal/i18n/` tells which ones are
  missing (in doubt, leave the English text and say so in the pull request).
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):
  `feat:`, `fix:`, `docs:`, `test:`, `chore:`… and `!` for a breaking change
  (`feat!:`).
- A change users can notice gets a line under *Unreleased* in
  [CHANGELOG.md](CHANGELOG.md).
- New behavior comes with tests. The whole web interface is in `web/` (Svelte and Tailwind)
  and compiled into `internal/web/dist/`, which is committed.

## Pull requests

1. Fork the repository and create a branch from `main`.
2. Make your change, with its tests and its changelog line.
3. Open the pull request against `main` and fill in the template. The CI must pass
   before it is merged.

## Releases

Versions follow [Semantic Versioning](https://semver.org/). To release, the maintainer
moves *Unreleased* to a new version in the changelog and pushes a `vX.Y.Z` tag: the CI
publishes the Docker image (`:X.Y.Z`, `:X.Y`, `:X`, `:latest`) and the GitHub release,
whose notes come from the changelog.

By contributing, you agree that your contribution is published under the project's
[MIT license](LICENSE), and to follow the [code of conduct](CODE_OF_CONDUCT.md).
