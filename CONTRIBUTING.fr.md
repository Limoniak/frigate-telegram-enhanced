# Contribuer

[English](CONTRIBUTING.md) · **Français**

Merci de votre intérêt pour frigate-telegram-enhanced ! Les signalements de bugs, les
idées, les traductions et les pull requests sont les bienvenus.

## Signaler un bug ou proposer une fonctionnalité

Ouvrez une [issue](https://github.com/Limoniak/frigate-telegram-enhanced/issues/new/choose)
et choisissez le formulaire qui convient. Pour un bug, la version du service, la version
de Frigate et le mode (`reviews` ou `events`) aident beaucoup ; les journaux du service
(`docker logs frigate-telegram-enhanced`) encore plus. Retirez-en le token du bot, les
mots de passe et les adresses avant de les publier.

Un problème de sécurité ne se signale pas dans une issue publique : voir
[SECURITY.md](SECURITY.md).

## Traduire

Une langue tient dans un fichier, `internal/i18n/locales/<code>.json` : voir
[Traduire](README.fr.md#traduire). Vous pouvez ouvrir une pull request avec le fichier,
ou le joindre à une issue *Traduction* si vous n'êtes pas familier de Git.

## Modifier le code

Il faut Go (la version de `go.mod`) et, pour les tests de l'interface web, Chrome.

```bash
go vet ./...
go test ./...
CHROME_PATH=/chemin/vers/chrome go test -tags browser -run Browser ./internal/web/
go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

La CI lance les mêmes vérifications sur chaque pull request.

Les pages d'installation et de connexion sont construites à partir de `web/` (Svelte et
Tailwind). `go build` et `go test` n'ont pas besoin de Node : le résultat du build,
`internal/web/dist/`, est versionné. Après une modification dans `web/` :

```bash
cd web
npm ci
npm test
npm run build      # met à jour internal/web/dist/, à committer avec votre changement
npm run dev        # rechargement à chaud, avec l'API d'un service sur le port 8431
```

`npm run dev` sert la page d'installation sur <http://localhost:5173/setup.html>.

La CI échoue quand `internal/web/dist/` ne correspond pas à `web/`. Les pull requests npm
de Dependabot ne reconstruisent pas `dist/` : quand la vérification échoue sur l'une
d'elles, un mainteneur lance `npm run build` sur sa branche et pousse le résultat.

Conventions :

- **Anglais** dans le code, les commentaires, les tests et les messages de commit. Les
  textes montrés aux utilisateurs passent par les catalogues : `T("…")` en Go et en
  JavaScript, `data-t="…"` en HTML. Chaque catalogue doit traduire chaque texte :
  `go test ./internal/i18n/` indique ceux qui manquent (dans le doute, laissez le texte
  anglais et signalez-le dans la pull request).
- Les messages de commit suivent [Conventional Commits](https://www.conventionalcommits.org/fr/) :
  `feat:`, `fix:`, `docs:`, `test:`, `chore:`… et `!` pour un changement cassant
  (`feat!:`).
- Un changement visible par les utilisateurs a sa ligne sous *Unreleased* dans
  [CHANGELOG.md](CHANGELOG.md) (en anglais).
- Un nouveau comportement arrive avec ses tests. L'interface web principale n'a pas
  d'étape de build : `ui.html`, `ui.css` et `ui.js` sont intégrés tels quels. Seules les
  pages d'installation et de connexion viennent de `web/`.

## Pull requests

1. Forkez le dépôt et créez une branche depuis `main`.
2. Faites votre modification, avec ses tests et sa ligne de changelog.
3. Ouvrez la pull request vers `main` et remplissez le modèle. La CI doit passer avant
   la fusion.

## Versions

Les versions suivent le [versionnage sémantique](https://semver.org/lang/fr/). Pour
publier, le mainteneur déplace *Unreleased* vers une nouvelle version dans le changelog
et pousse un tag `vX.Y.Z` : la CI publie l'image Docker (`:X.Y.Z`, `:X.Y`, `:X`,
`:latest`) et la release GitHub, dont les notes viennent du changelog.

En contribuant, vous acceptez que votre contribution soit publiée sous la
[licence MIT](LICENSE) du projet, et de respecter le [code de conduite](CODE_OF_CONDUCT.md).
