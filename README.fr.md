# frigate-telegram

[English](README.md) · **Français**

Notifications Telegram pour [Frigate NVR](https://frigate.video) : un snapshot dès la
détection, puis le clip vidéo en réponse, avec des filtres fins, une interface web pour
tout régler en quelques clics et un pilotage depuis Telegram.

<p align="center">
  <img src="docs/screenshots/overview.png" width="820" alt="Interface web : modèles de notification avec aperçu du message Telegram, et questions à choix simples">
</p>

## Fonctionnalités

- Mode **events** (un message par objet détecté) ou **reviews** (alertes regroupées, Frigate ≥ 0.14)
- Snapshot immédiat, clip MP4 en réponse, et GIF en option ; au-delà de 50 Mo, un lien vers le clip
- Description GenAI de Frigate ajoutée à la légende dès qu'elle est disponible
- Filtres par caméra, objet, zone, score minimum et sévérité ; cooldown ; plages silencieuses et plages coupées
- **Interface web** pour régler tout cela sans éditer de YAML, appliquée sans redémarrage
- Plusieurs destinataires avec routage par caméra
- Boutons 🔇 *couper la caméra 1 h*, ⏸ *pause 30 min*, 🎬 *clip*
- Commandes `/pause`, `/resume`, `/status`, `/cameras`, `/snapshot`, `/last`
- État persistant, `/healthz`, métriques Prometheus, image distroless multi-arch (amd64 et arm64)

## L'interface web en images

**Choisir un modèle de notification.** Quatre modèles — *Photo + vidéo*, *Photo seule*,
*Photo + GIF*, *Texte seul* — chacun avec un aperçu du message tel qu'il arrivera dans
Telegram. Un clic, et toutes les caméras l'adoptent. En dessous, quelques questions à
choix simples : quoi signaler, à quelle fréquence, et que faire la nuit (capture
ci-dessus).

**Régler une caméra.** Un interrupteur par caméra ; en l'ouvrant, on lui donne un autre
modèle, d'autres objets ou seulement certaines zones — sinon elle suit les choix
généraux. *Tester cette caméra* envoie une vraie notification d'exemple.

<p align="center">
  <img src="docs/screenshots/cameras.png" width="820" alt="Caméra ouverte : modèle, objets et zones, bouton de test">
</p>

**Comprendre pourquoi une notification n'est pas arrivée.** L'activité récente liste les
dernières détections, avec leur issue : ✅ envoyée, ou ⛔ ignorée et pourquoi (hors zone,
score trop bas, déjà signalé il y a peu…), avec un lien direct vers la caméra à régler.

<p align="center">
  <img src="docs/screenshots/activity.png" width="820" alt="Activité récente : détections envoyées ou ignorées, avec la raison">
</p>

**Sur téléphone, et en mode sombre.** La page s'adapte à l'écran et suit le thème du système.

<p align="center">
  <img src="docs/screenshots/mobile.png" width="260" alt="Interface sur téléphone">
  &nbsp;&nbsp;
  <img src="docs/screenshots/dark.png" width="540" alt="Interface en mode sombre">
</p>

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

Ouvrez ensuite `http://127.0.0.1:8080/` pour régler les notifications, puis
*M'envoyer un exemple* pour vérifier que tout arrive bien dans Telegram.

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

## Interface web

`http://127.0.0.1:8080/` sert une page de réglage des notifications, pensée pour se
régler en quelques clics :

- **Modèles de notification** : *Photo + vidéo*, *Photo seule*, *Photo + GIF* ou
  *Texte seul*, chacun avec un aperçu du message tel qu'il arrivera dans Telegram.
- **Questions simples** : quoi signaler (personnes, personnes et voitures, tout), à
  quelle fréquence au plus, et quoi faire la nuit (comme le jour, sans son, rien).
- **Caméras** : un interrupteur par caméra ; en l'ouvrant, on lui donne un autre modèle
  ou d'autres objets, sinon elle suit les choix du haut.
- **Où ?** : pour une caméra qui a des zones dans Frigate, « Partout » ou seulement
  certaines zones, en un clic.
- **Essai** : *M'envoyer un exemple* envoie une vraie notification de test aux
  destinataires, avec l'image en direct de la caméra et les réglages enregistrés.
- **Pause** : un bandeau indique si les notifications sont actives ; pause de 30 min,
  1 h, 8 h ou jusqu'à reprise, et réactivation des caméras coupées depuis Telegram.
- **Activité récente** : les 50 dernières détections avec leur miniature et leur
  issue — ✅ envoyée, ou ⛔ ignorée avec la raison en clair (hors zone, score trop bas,
  déjà signalé il y a peu…) et un lien pour régler la caméra concernée. L'historique est
  gardé en mémoire et repart de zéro au redémarrage du service.
- **Alertes de cohérence** : une caméra réglée pour signaler un objet que Frigate n'y
  suit pas (absent de `objects.track`) est signalée.
- **Réglages avancés** (repliés) : tous les réglages en détail — zones, scores,
  plages horaires sur mesure, délais… — en global puis caméra par caméra. Les zones et
  les objets proposés viennent de l'API de Frigate.

Un enregistrement prend effet **immédiatement**, sans redémarrage, et n'est accepté que
s'il est valide — un réglage refusé laisse le service sur les précédents.

Les réglages sont écrits dans `/data/notify.yml`, à côté de l'état, et **remplacent les
sections `notify` et `cameras` de `config.yml`** tant que ce fichier existe ; le bouton
*Revenir aux réglages de config.yml* le supprime. `config.yml` reste la source des
secrets, des `${VAR}` et des commentaires, et n'est jamais réécrit — c'est d'ailleurs
nécessaire, le conteneur le monte en lecture seule.

### Accès et sécurité

```yaml
web:
  enabled: true                  # false pour ne pas servir l'interface du tout
  password: "${WEB_PASSWORD:-}"  # vide = aucune authentification
  allowed_hosts: []              # noms d'hôte acceptés sans mot de passe (ex. [nas.lan])
  protect_metrics: false         # true = /metrics exige aussi le mot de passe
```

Sans mot de passe, l'interface est ouverte à quiconque atteint le port : le
`docker-compose.yml` fourni ne publie `8080` que sur `127.0.0.1`. Pour y accéder depuis
le réseau ou un reverse proxy, renseigner `WEB_PASSWORD` dans `.env`.

Sans mot de passe, l'interface n'accepte en outre que les requêtes adressées à une
adresse IP ou à `localhost` (`http://192.168.1.10:8080/` fonctionne,
`http://nas.lan:8080/` est refusé en 403). Cela bloque le *rebinding DNS*, où un site
malveillant ouvert dans votre navigateur fait pointer son propre domaine vers
`127.0.0.1` pour piloter l'interface à votre insu. Pour passer par un nom d'hôte,
l'ajouter à `web.allowed_hosts`, ou définir un mot de passe (qui lève ce contrôle).

L'authentification ne couvre que l'interface : `/healthz` reste toujours libre pour la
sonde du conteneur, et `/metrics` aussi, sauf avec `protect_metrics: true`. Si le port
est exposé au réseau, pensez-y : les compteurs par caméra et par objet révèlent quand
il y a de l'activité chez vous. Prometheus s'authentifie alors avec `basic_auth`
(nom d'utilisateur libre, mot de passe `WEB_PASSWORD`).

## Commandes Telegram

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
- `/healthz` n'est jamais protégée ; `/metrics` l'est par `web.password` si `web.protect_metrics: true`

## Dépannage

- **Aucune notification** : ouvrir l'*Activité récente* de l'interface web, qui donne la
  raison de chaque détection ignorée. Sinon, `log_level: debug`, puis vérifier
  `ft_events_received_total` et `ft_events_filtered_total{reason=...}` sur `/metrics`.
- **Clip manquant** : augmenter `clip_delay` (Frigate n'a pas encore fini d'écrire le clip).
- **Interface en 403 « hôte non autorisé »** : voir [Accès et sécurité](#accès-et-sécurité)
  — ajouter le nom d'hôte à `web.allowed_hosts` ou définir un mot de passe.
- **`permission denied` sur `/data`** : voir le `chown` de l'installation. L'interface web ne peut pas enregistrer sans ce droit.
- **Réglages de l'interface ignorés au démarrage** : le journal indique pourquoi `/data/notify.yml` a été écarté (un chat supprimé de `telegram.chats`, par exemple). Le service repart alors sur `config.yml`.

## Développement

```bash
go test ./...
go build ./cmd/frigate-telegram
```
