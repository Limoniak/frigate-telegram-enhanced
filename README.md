# frigate-telegram

Notifications Telegram pour [Frigate NVR](https://frigate.video) : un snapshot dès la détection, puis le clip vidéo en réponse, avec des filtres fins et un pilotage depuis Telegram.

## Fonctionnalités

- Mode **events** (un message par objet détecté) ou **reviews** (alertes regroupées, Frigate ≥ 0.14)
- Snapshot immédiat, clip MP4 en réponse, et GIF en option ; au-delà de 50 Mo, un lien vers le clip
- Description GenAI de Frigate ajoutée à la légende dès qu'elle est disponible
- Filtres par caméra, objet, zone, score minimum et sévérité ; cooldown ; plages silencieuses et plages coupées
- Plusieurs destinataires avec routage par caméra
- Boutons 🔇 *couper la caméra 1 h*, ⏸ *pause 30 min*, 🎬 *clip*
- Commandes `/pause`, `/resume`, `/status`, `/cameras`, `/snapshot`, `/last`
- État persistant, `/healthz`, métriques Prometheus, image distroless multi-arch (amd64 et arm64)

## Prérequis

1. **MQTT activé dans Frigate** (`config.yml` de Frigate) :

   ```yaml
   mqtt:
     enabled: true
     host: mosquitto
     user: frigate
     password: ...
   ```

2. **Un bot Telegram** : écrire à [@BotFather](https://t.me/BotFather), `/newbot`, puis noter le token.

3. **Votre identifiant Telegram** : écrire un message à votre bot, puis ouvrir
   `https://api.telegram.org/bot<TOKEN>/getUpdates` et relever `message.from.id`.
   Pour un groupe : ajouter le bot au groupe, y écrire un message et relever `message.chat.id` (négatif).

## Installation

```bash
mkdir -p config data
cp config.example.yml config/config.yml   # puis l'adapter
cp .env.example .env                      # puis y mettre le token
sudo chown 65532:65532 data               # l'image tourne en utilisateur non-root (uid 65532)
docker compose up -d
docker compose logs -f
```

L'image est construite et publiée par GitHub Actions sur GitHub Container Registry :
`ghcr.io/limoniak/frigate-telegram` (amd64 et arm64).

- `:latest` et `:X.Y.Z` — publiés à chaque tag `v*`
- `:main` et `:sha-<court>` — publiés à chaque push sur `main`

Pour construire localement à la place, commenter `image:` et décommenter `build: .`
dans [`docker-compose.yml`](docker-compose.yml), puis `docker compose up -d --build`.

## Configuration

Tout est documenté dans [`config.example.yml`](config.example.yml). Points clés :

- Les réglages de `notify` s'appliquent à toutes les caméras ; une entrée dans `cameras` remplace champ par champ.
- `zones` : ne notifie que si l'objet est **entré** dans une des zones listées.
- `quiet_hours` : notification sans son ; `off_hours` : aucune notification.
- Mode `reviews` : `severity: [alert]` suit la configuration `review.alerts` de Frigate.
- Les secrets passent par `.env` (`${TELEGRAM_TOKEN}`…). Mettre les valeurs entre guillemets dans le YAML.

## Commandes

| Commande | Effet |
|---|---|
| `/pause [durée] [caméra]` | Pause globale ou d'une caméra (1 h par défaut, `0` = jusqu'à `/resume`). Durées : `30m`, `2h`, `1d` |
| `/resume [caméra]` | Reprend une caméra, ou tout sans argument |
| `/status` | Connexion MQTT, pauses actives, notifications sur 24 h |
| `/cameras` | Caméras et leur état |
| `/snapshot [caméra]` | Image en direct |
| `/last [caméra]` | Dernier événement (snapshot et clip) |

Seuls les utilisateurs listés dans `telegram.admins` peuvent utiliser les commandes et les boutons.

## Supervision

- `GET /healthz` : 200 si MQTT est connecté et Telegram joignable (utilisé par le `HEALTHCHECK` Docker)
- `GET /metrics` : métriques Prometheus préfixées par `ft_`

## Dépannage

- **Aucune notification** : `log_level: debug`, puis vérifier `ft_events_received_total` et `ft_events_filtered_total{reason=...}` sur `/metrics`.
- **Clip manquant** : augmenter `clip_delay` (Frigate n'a pas encore fini d'écrire le clip).
- **`permission denied` sur `/data`** : voir le `chown` de l'installation.

## Développement

```bash
go test ./...
go build ./cmd/frigate-telegram
```
