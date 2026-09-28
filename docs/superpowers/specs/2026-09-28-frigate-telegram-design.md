# frigate-telegram — Spécification

Date : 2026-09-28
Statut : en revue

## 1. Objectif

Service Go, distribué en image Docker, qui envoie sur Telegram les détections de
Frigate NVR (snapshot immédiat, puis clip vidéo) avec un filtrage fin et un
pilotage depuis Telegram (pause, coupure par caméra, snapshot à la demande).

Hors périmètre : présence automatique (Home Assistant), interface web, stockage
des médias.

## 2. Architecture

```
                 MQTT (frigate/events | frigate/reviews | frigate/tracked_object_update)
Frigate ───────────────────────────────────────────────▶ ┌────────────────────────┐
   ▲                                                      │    frigate-telegram     │
   │  HTTP API (snapshot, clip, gif, latest.jpg, config)  │                        │ ──▶ Telegram Bot API
   └──────────────────────────────────────────────────────│  :8080 /healthz /metrics│ ◀── getUpdates (commandes, boutons)
                                                          └────────────────────────┘
                                                               │ /data/state.json
```

Un seul processus. Goroutines principales :

| Goroutine | Rôle |
|---|---|
| MQTT listener | Reçoit et décode les messages, les pousse dans le canal d'événements |
| Notifier | Suit le cycle de vie de chaque événement, applique le filtre, lance les envois |
| Media workers (pool, 4 par défaut) | Téléchargent snapshot / clip / gif depuis Frigate |
| Sender par chat | File d'envoi par chat Telegram, limitée en débit |
| Bot poller | Long polling `getUpdates` : commandes et callbacks des boutons |
| HTTP server | `/healthz`, `/metrics` |

Arrêt propre sur SIGTERM : on arrête de consommer MQTT, on vide les files
(timeout 10 s), on sauvegarde l'état.

## 3. Packages

```
cmd/frigate-telegram/main.go   assemblage, signaux, flags (-config)
internal/config                chargement YAML, expansion ${ENV}, défauts, validation, fusion global/caméra
internal/frigate               types des payloads MQTT + parsing ; client HTTP (auth JWT, retries)
internal/mqttsub               connexion paho, abonnements, reconnexion
internal/filter                moteur de décision PUR (aucune E/S, horloge injectée)
internal/state                 pauses / coupures / cooldowns ; persistance JSON atomique
internal/telegram              client Bot API minimal (multipart), rate limiter, retries 429/5xx
internal/notifier              cycle de vie des événements, pipeline snapshot → clip → description
internal/bot                   commandes et callbacks
internal/server                healthz + métriques Prometheus
```

Dépendances externes : `github.com/eclipse/paho.mqtt.golang`, `gopkg.in/yaml.v3`,
`github.com/prometheus/client_golang`. Tests d'intégration :
`github.com/mochi-mqtt/server/v2` (broker embarqué). Le reste en stdlib
(`net/http`, `log/slog`, `mime/multipart`).

Le client Telegram est écrit à la main (~300 lignes) plutôt qu'une lib, pour
maîtriser l'upload multipart depuis un fichier, les retries et le rate limiting.

## 4. Configuration

Fichier YAML (`/config/config.yml`). Toute valeur `${VAR}` est remplacée par la
variable d'environnement (erreur au démarrage si elle est absente). Les secrets
passent donc par l'environnement.

```yaml
timezone: Europe/Paris
mode: events                # events | reviews

frigate:
  url: http://frigate:5000  # URL interne pour l'API
  external_url: ""          # URL publique pour les liens dans les messages (optionnelle)
  username: ""              # si auth activée (port 8971)
  password: ${FRIGATE_PASSWORD}
  insecure_skip_verify: false

mqtt:
  broker: tcp://mosquitto:1883   # ou ssl://...:8883
  username: ""
  password: ${MQTT_PASSWORD}
  client_id: frigate-telegram
  topic_prefix: frigate

telegram:
  token: ${TELEGRAM_TOKEN}
  admins: [123456789]            # user IDs autorisés pour commandes et boutons
  chats:
    moi: 123456789
    famille: -1001234567890

notify:                          # valeurs globales, surchargeables par caméra
  chats: [moi]
  labels: [person, car]          # vide = tous
  zones: []                      # vide = pas de contrainte de zone
  min_score: 0.7
  cooldown: 60s                  # par couple caméra+label
  ignore_stationary: true
  severity: [alert]              # mode reviews uniquement : alert, detection
  snapshot: true
  clip: true
  gif: false
  genai_description: true
  clip_delay: 5s                 # attente après "end" avant de récupérer le clip
  quiet_hours: [{from: "22:00", to: "07:00"}]   # notifications sans son
  off_hours: []                                  # aucune notification

cameras:
  jardin:
    zones: [allee, portail]
    chats: [moi, famille]
  garage:
    labels: [person]
    min_score: 0.8
  salon:
    enabled: false

state_file: /data/state.json
http_listen: ":8080"
log_level: info                  # debug | info | warn | error
```

Règles :
- une caméra absente de `cameras` hérite de `notify` ; une caméra listée fusionne
  ses champs par-dessus `notify` (remplacement champ par champ, pas de fusion de listes) ;
- `min_score` accepte aussi une map par label : `min_score: {person: 0.7, car: 0.85}` ;
- validation au démarrage : chats référencés existants, durées et horaires valides,
  `mode` connu, token présent. Toute erreur arrête le programme avec un message clair.

## 5. Flux de notification

### 5.1 Mode `events` (topic `frigate/events`)

Payload : `{type: new|update|end, before: {...}, after: {id, camera, label,
sub_label, top_score, score, entered_zones, current_zones, stationary,
false_positive, has_snapshot, has_clip, start_time, end_time}}`.

Le Notifier tient une table `id → tracked{notified, messages map[chat]msgID, lastSeen}`.

1. `new` ou `update` pas encore notifié → `filter.Evaluate(after, now)`.
   Évaluer aussi sur `update` permet de notifier quand l'objet entre dans une zone
   requise ou dépasse le score après coup.
2. Si la décision est positive : on marque l'événement notifié, on enregistre le
   cooldown, puis on télécharge `/api/events/{id}/snapshot.jpg?bbox=1` et on envoie
   `sendPhoto` à chaque chat cible avec la légende et les boutons. Les `message_id`
   sont conservés.
3. `end` d'un événement notifié → attente de `clip_delay`, puis
   `/api/events/{id}/clip.mp4` avec 3 essais (backoff 5/10/20 s si 404/500).
   Envoi par `sendVideo` en réponse au snapshot (`reply_parameters`), avec
   `supports_streaming`.
4. `gif: true` → `/api/events/{id}/preview.gif` envoyé via `sendAnimation`, en réponse aussi.
5. Nettoyage de l'entrée à `end`, plus un balayage TTL de 1 h pour les événements
   dont le `end` n'arrive jamais.

### 5.2 Mode `reviews` (topic `frigate/reviews`, Frigate ≥ 0.14)

Payload `after : {id, camera, severity, start_time, end_time, data: {detections,
objects, sub_labels, zones}}`. Mêmes étapes, avec ces adaptations :
- filtre sur `severity`, `data.objects` (labels) et `data.zones` ;
- snapshot = celui de `data.detections[0]` (`/api/events/{id}/snapshot.jpg`) ;
- clip = `/api/{camera}/start/{start_time}/end/{end_time}/clip.mp4` ;
- gif = `/api/review/{id}/preview?format=gif`.

### 5.3 Description GenAI

Topic `frigate/tracked_object_update`, `type: description`. Si
`genai_description` est activé et que l'`id` (ou, en mode reviews, une des
`detections`) correspond à un message envoyé, on appelle `editMessageCaption`
pour ajouter la description. Sans correspondance, le message est ignoré.

### 5.4 Légende

```
🚶 Personne — jardin
📍 allee, portail · 87 %
🕑 14:32:05
<description GenAI si présente>
🔗 Ouvrir dans Frigate          (si external_url)
```
Emoji et traduction FR pour les labels courants (person, car, dog, cat, bicycle,
motorcycle, bird, package), et le label brut sinon. `sub_label` (visage, plaque)
est affiché s'il est présent.

### 5.5 Médias et limites Telegram

- Le clip est écrit dans un **fichier temporaire** (`os.CreateTemp`, `/tmp`,
  monté en tmpfs dans le compose), jamais chargé entièrement en RAM. Le fichier
  est réutilisé pour les retries et pour chaque chat, puis supprimé.
- Si le clip dépasse 50 Mo (limite d'upload de l'API Bot), le téléchargement est
  interrompu et on envoie un message texte avec le lien vers le clip
  (`external_url` si défini, sinon on ne met pas de lien).
- Les photos (limite 10 Mo) sont gardées en mémoire (quelques centaines de Ko).
- Pour plusieurs chats, le premier envoi uploade le fichier et les suivants
  réutilisent le `file_id` renvoyé par Telegram : un seul upload par média.

## 6. Moteur de filtre (`internal/filter`)

```go
type Input struct {
    Camera, Label, SubLabel string
    Score        float64
    Zones        []string // entered_zones (events) ou data.zones (reviews)
    Severity     string   // reviews
    Stationary, FalsePositive bool
}
type Decision struct {
    Notify bool
    Silent bool
    Chats  []string
    Reason string // "camera_disabled", "label", "score", "zone", "paused", "muted", "off_hours", "cooldown", "stationary", "false_positive", "severity"
}
func (e *Engine) Evaluate(in Input, now time.Time) Decision
```

Ordre d'évaluation (la première règle qui échoue donne `Reason`) :
caméra activée → faux positif → immobile → sévérité → label → score → zone →
pause globale → coupure de la caméra → `off_hours` → cooldown.
`Silent = true` si `now` tombe dans `quiet_hours`. Les plages qui passent minuit
sont gérées, et tous les horaires sont interprétés dans `timezone`.

Le moteur lit l'état (pauses, coupures, cooldowns) via une interface, ce qui
permet de le tester sans E/S avec une horloge fixe.

## 7. Bot Telegram

Commandes (enregistrées via `setMyCommands` au démarrage) :

| Commande | Effet |
|---|---|
| `/pause [durée] [caméra]` | Pause globale ou d'une caméra. Durée par défaut 1 h ; `0` = jusqu'à `/resume`. Formats : `30m`, `2h`, `1d` |
| `/resume [caméra]` | Lève la pause globale ou celle d'une caméra |
| `/status` | État : connexion MQTT, pauses actives avec heure de fin, nombre de notifications sur 24 h |
| `/cameras` | Liste des caméras (depuis `/api/config`) avec leur état |
| `/snapshot <caméra>` | Image en direct (`/api/{camera}/latest.jpg`). Sans argument : clavier de sélection |
| `/last [caméra]` | Renvoie le dernier événement (snapshot et clip) via `/api/events?limit=1` |
| `/help` | Aide |

Boutons inline sous chaque notification (`callback_data` ≤ 64 octets) :
- `🔇 1 h` → `m:<caméra>:3600` (coupe la caméra pendant 1 h)
- `⏸ 30 min` → `p:1800` (pause globale)
- `🎬 Clip` → `c:<event_id>` (renvoie le clip ; utile s'il est arrivé en retard ou avait échoué)

Sécurité : les commandes et callbacks ne sont acceptés que si `from.id` est dans
`telegram.admins`, sinon ils sont ignorés (avec un log et un `answerCallbackQuery`
« non autorisé »). Cela vaut aussi dans les groupes.

## 8. État persistant (`internal/state`)

```json
{"global_pause_until": "...", "camera_mutes": {"jardin": "..."}, "cooldowns": {"jardin/person": "..."}}
```
Écriture atomique (fichier temporaire + `rename`) après chaque modification de
pause ou de coupure, et toutes les 30 s si des cooldowns ont changé. Les entrées
expirées sont purgées au chargement. Si le fichier est corrompu, on logue un
avertissement et on démarre avec un état vide.

## 9. Fiabilité et performance

- **MQTT** : `AutoReconnect`, `ConnectRetry`, backoff plafonné à 30 s, QoS 0 pour
  suivre Frigate et `CleanSession`. Réabonnement dans `OnConnect`.
- **Frigate HTTP** : un `http.Client` partagé (keep-alive), timeout de 10 s pour
  les images et de 120 s pour les clips. Avec auth : `POST /api/login`, cookie
  `frigate_token` conservé dans un jar, nouvelle connexion automatique sur 401.
- **Telegram** : retry sur 429 (en respectant `retry_after`) et sur 5xx ou erreur
  réseau (3 essais, backoff exponentiel). Aucun retry sur les autres 4xx, qui sont
  logués. Rate limiter : token bucket global de 30/s, plus 1/s par chat privé et
  20/min par groupe (IDs négatifs).
- **Isolation** : un sender par chat, donc un chat lent ou en erreur ne bloque
  pas les autres. Le canal d'événements est bufferisé (256). S'il est saturé, on
  jette le message et on incrémente `events_dropped_total` pour ne jamais bloquer
  le callback MQTT.
- **Ressources visées** : moins de 15 Mo de RAM au repos, image de moins de 20 Mo.

## 10. Observabilité

- Logs `slog` en JSON sur stdout, avec les champs `camera`, `label`, `event_id`,
  `chat` et `reason`.
- `/healthz` : 200 si MQTT est connecté et que le dernier `getUpdates` a réussi il
  y a moins de 2 min, 503 sinon. Utilisé par le `HEALTHCHECK` Docker via le
  binaire lui-même (`frigate-telegram -healthcheck`), car distroless n'a ni curl
  ni wget.
- `/metrics` : `ft_events_received_total{camera,label}`,
  `ft_events_filtered_total{reason}`, `ft_notifications_sent_total{kind}`
  (photo, video, animation, text), `ft_telegram_errors_total{method,code}`,
  `ft_events_dropped_total`, `ft_mqtt_connected`, `ft_media_download_seconds`
  (histogramme).

## 11. Livraison

- `Dockerfile` multi-stage : `golang:1.25-alpine` avec
  `CGO_ENABLED=0 -trimpath -ldflags="-s -w"`, puis
  `gcr.io/distroless/static-debian12:nonroot`. Données fuseau horaire
  embarquées (`-tags timetzdata`). Utilisateur non-root.
- `docker-compose.yml` d'exemple : volumes `./config:/config:ro` et
  `./data:/data`, `tmpfs: /tmp`, `.env` pour les secrets, `restart: unless-stopped`.
- `config.example.yml` commenté et `README.md` en français (création du bot avec
  BotFather, récupération du chat_id, configuration MQTT de Frigate).
- `.github/workflows/docker.yml` : tests, puis `docker buildx` pour
  `linux/amd64` et `linux/arm64`, publication sur GHCR aux tags `v*`.

## 12. Tests

- **Unitaires** : `filter` (table-driven : chaque `Reason`, plages horaires
  passant minuit, fuseau, cooldown, fusion global/caméra), `config` (défauts,
  expansion d'environnement, erreurs de validation), `frigate` (parsing de
  payloads réels d'events, reviews et tracked_object_update en fixtures JSON),
  `state` (persistance, expiration, fichier corrompu), `telegram` (multipart,
  retry 429 avec `retry_after`, rate limiter), `bot` (parsing des commandes et
  des durées, autorisation).
- **Intégration** (`internal/notifier`) : broker mochi-mqtt embarqué, faux
  Frigate et faux Telegram (`httptest`). Scénarios : new → photo ; end → vidéo en
  réponse ; entrée en zone sur un update ; cooldown ; pause via bouton ; clip
  indisponible puis disponible ; clip de plus de 50 Mo remplacé par un lien ;
  édition de la description GenAI ; mode reviews.
- `go test -race ./...` en CI.

## 13. Environnement de développement

Go et Docker ne sont pas installés sur le poste de développement actuel.
Il faudra soit installer Go 1.25 (`winget install GoLang.Go`), soit exécuter les
tests dans un conteneur `golang:1.25`.
