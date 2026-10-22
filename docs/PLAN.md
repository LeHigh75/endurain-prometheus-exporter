# Plan de réalisation

État : le rôle SQL, les requêtes, le collecteur Go et l’authentification Bearer facultative sont implémentés. Les tests incluent PostgreSQL 18, plusieurs utilisateurs et sports, les permissions, les fenêtres temporelles, les erreurs et la reprise. Docker, Compose et une CI sont fournis. Le déploiement permanent et le dashboard restent à réaliser ; les éléments ci-dessous décrivent le contrat et ses validations.

## 1. Contrat de données

Le périmètre retenu est un exporter Go de statistiques individuelles : chaque métrique métier porte un label `user_id`. L’utilisateur est la dimension principale ; le sport permet de détailler ses activités. Le dépôt est public ; l’administrateur choisit la politique d’accès aux métriques.

La prise en charge de plusieurs utilisateurs et de plusieurs sports est une exigence dès la première version. Un utilisateur peut pratiquer plusieurs sports et un sport peut appartenir aux activités de plusieurs utilisateurs. Le volume observé lors d’une inspection ne limite pas ce périmètre.

À confirmer avant l’implémentation :

- Volume représentatif pour les tests de charge et fréquence d’import.
- Besoin de suivi à partir de la première collecte ou de consultation de l’historique complet.
- Destination des données : Prometheus local et, si souhaité, envoi vers un service distant.

Règles de la première version : tous les comptes sont inclus, y compris désactivés ; les activités privées contribuent aux agrégats individuels. Les activités avec `is_hidden = true` sont exclues. L’exporter ne déduplique pas les autres activités. Lorsqu’il est configuré, le token de scrape donne accès aux métriques de tous les utilisateurs ; sinon, le contrôle d’accès relève de l’installation.

Le schéma étudié est celui du tag **v0.19.4**, pas la branche de développement. La table [`activities`](https://github.com/endurain-project/endurain/blob/v0.19.4/backend/app/activities/activity/models.py) contient notamment :

| Colonne | Utilisation proposée |
| --- | --- |
| `user_id` | Identifiant de l’utilisateur |
| `activity_type` | Identifiant du sport |
| `distance` | Distance en mètres |
| `total_timer_time` | Durée active en secondes |
| `total_elapsed_time` | Durée écoulée, distincte de la durée active |
| `elevation_gain` | Dénivelé positif en mètres, facultatif |
| `calories` | Énergie en kilocalories, facultative |
| `start_time`, `end_time` | Dates de l’activité |
| `is_hidden`, `visibility` | Règles d’inclusion à préciser |

Vérifier les types réels, les migrations et les index sur l’instance cible avec des requêtes de métadonnées. Comparer ensuite des agrégats à l’interface Endurain, sans extraire de notes privées, traces GPS ou identifiants d’intégration.

## 2. Accès PostgreSQL

- Créer un rôle de connexion dédié, sans privilèges d’administration, création, écriture ou appartenance au rôle applicatif.
- Accorder `CONNECT`, `USAGE` sur le schéma et `SELECT` sur les seules colonnes nécessaires. Ne pas accorder de lecture globale des tables présentes ou futures.
- Conserver les identifiants dans un gestionnaire de secrets en production ; prévoir la lecture depuis un fichier secret, sans afficher la chaîne de connexion dans les logs.
- Définir des délais pour la connexion et les requêtes, un petit pool et des transactions explicitement en lecture seule.
- Garder PostgreSQL sur un réseau privé. Valider le transport utilisé et activer une vérification TLS lorsqu’il traverse un réseau qui l’exige.
- Le provisionnement des droits relève d’une étape d’administration séparée. Le binaire ne crée ni rôle, ni table, ni migration.

Tests attendus : les agrégats sont lisibles ; écritures, DDL et lecture de colonnes privées sont refusés. Le mode transactionnel en lecture seule complète les permissions, il ne les remplace pas.

## 3. Contrat Prometheus proposé

Les métriques métier sont des **gauges** : une modification ou suppression d’activité peut faire baisser les totaux. Ne pas utiliser le suffixe `_total`, réservé aux counters, ni `rate()` ou `increase()` pour calculer le volume sportif depuis ces snapshots.

| Métrique proposée | Sens | Labels |
| --- | --- | --- |
| `endurain_user_info` | Présence d’un utilisateur, même sans activité | `user_id` |
| `endurain_activities` | Nombre d’activités présentes | `user_id`, `sport_id`, `period` |
| `endurain_activity_distance_meters` | Distance cumulée | `user_id`, `sport_id`, `period` |
| `endurain_activity_duration_seconds` | Durée active cumulée | `user_id`, `sport_id`, `period` |
| `endurain_activity_elevation_gain_meters` | Dénivelé positif, si toutes les valeurs de la période sont connues | `user_id`, `sport_id`, `period` |
| `endurain_activity_energy_joules` | Énergie, si toutes les valeurs de la période sont connues, convertie depuis les kcal | `user_id`, `sport_id`, `period` |
| `endurain_last_activity_timestamp_seconds` | Début de la dernière activité | `user_id`, `sport_id` |
| `endurain_up` | 1 si la collecte réussit, 0 sinon | aucun |
| `endurain_scrape_duration_seconds` | Durée de collecte | aucun |

Chaque couple observé `(user_id, activity_type)` possède ses propres agrégats SQL, calculés avec `GROUP BY user_id, activity_type`. `activity_type` devient le label `sport_id`. Aucun identifiant utilisateur ni liste de sports ne doit être codé en dur : les nouveaux couples doivent apparaître automatiquement aux collectes suivantes, y compris lorsqu’un code de sport est inconnu de l’exporter.

Exemple synthétique de séries indépendantes :

```prometheus
endurain_activity_distance_meters{user_id="1",sport_id="1",period="all"} 12000
endurain_activity_distance_meters{user_id="1",sport_id="2",period="all"} 5000
endurain_activity_distance_meters{user_id="2",sport_id="1",period="all"} 8000
```

Le dashboard Grafana sera centré sur la sélection d’un utilisateur, avec ses totaux tous sports confondus et sa répartition par sport. Par exemple, les distances de l’exemple donnent 17 000 m pour l’utilisateur `1` et 8 000 m pour l’utilisateur `2` :

```promql
sum by (user_id) (endurain_activity_distance_meters{period="all"})
```

Les totaux sont calculés en PromQL depuis les séries par sport. La date de dernière activité utilise `max by (user_id)` sur `endurain_last_activity_timestamp_seconds`.

`period` possède seulement trois valeurs : `all`, `7d`, `30d`. Les fenêtres de 7 et 30 jours sont des durées glissantes, calculées depuis un même instant UTC, bornes incluses et dates futures exclues ; elles ne représentent pas des semaines ou mois calendaires. `all` inclut toutes les activités présentes, y compris datées dans le futur. Les fenêtres se chevauchent : un dashboard doit en sélectionner une et ne pas les additionner.

Pour le calcul de fréquence, le nombre d’activités sur 7 jours donne directement un nombre de séances par semaine glissante. La conversion en kilomètres, heures ou kilocalories se fait dans Grafana ; 1 kcal thermochimique vaut 4 184 J.

Règles de justesse et de coût :

- Grouper en SQL, sans charger toutes les activités dans la mémoire du binaire.
- Distinguer une absence d’activité, une valeur facultative inconnue et une erreur de collecte. Pour le dénivelé et l’énergie, omettre l’agrégat du couple utilisateur/sport/période si une valeur manque ou si la période est vide. Ne pas présenter une somme partielle comme un total individuel : voir les requêtes Grafana dans `OPERATIONS.md`.
- Sur panne SQL ou erreur de schéma, exposer `endurain_up 0` et omettre les métriques métier du scrape concerné, sans réémettre un ancien résultat comme actuel.
- Produire les métriques depuis le résultat courant : les utilisateurs et sports supprimés doivent disparaître des scrapes suivants.
- En mode agrégé, aucun label par activité, date, titre, email, nom, lieu ou coordonnée GPS. Le mode facultatif `ACTIVITY_DETAILS=true` ajoute des gauges par activité avec identifiants et titre sur la seule métrique info, dans le même budget `MAX_SERIES`. Voir le README pour le contrat actuel.
- Borner le budget de séries métier avec `MAX_SERIES` (20 000 par défaut, hors deux métriques de santé). En cas de dépassement, exposer `endurain_up 0`, omettre les métriques métier et journaliser `series_budget` ; ne jamais tronquer silencieusement les utilisateurs.
- Des identifiants utilisateurs nécessitent une population bornée. Vérifier le volume avant de fixer le budget ; `topk()` dans Grafana ne réduit pas le nombre de séries ingérées.

Références : [conventions de nommage](https://prometheus.io/docs/practices/naming/) et [écriture d’exporters](https://prometheus.io/docs/instrumenting/writing_exporters/).

## 4. Implémentation Go

- Utiliser `net/http`, la bibliothèque officielle [`prometheus/client_golang`](https://github.com/prometheus/client_golang) et le pilote PostgreSQL [`pgx`](https://github.com/jackc/pgx), avec versions fixées dans `go.mod` et `go.sum`.
- Écrire un collecteur spécifique à Endurain : pas de moteur SQL configurable, d’ORM ou de plugin de collecte.
- Fournir `/metrics` et `/readyz` avec Bearer facultatif, et `/healthz` pour la vie du processus. Sans token, autoriser les endpoints et laisser l’administrateur choisir une protection externe. Un token configuré impose l’authentification ; un fichier secret configuré mais illisible ou vide doit bloquer le démarrage.
- Respecter l’annulation des requêtes, limiter le temps d’exécution HTTP et arrêter proprement le service.
- Collecter à la demande pour commencer. Ajouter un cache seulement si une mesure de charge le justifie, avec âge des données visible.
- Vérifier les plans d’exécution sur un volume représentatif avant de choisir la fréquence de scrape. Toute modification d’index de la base applicative doit être étudiée séparément.

## 5. Validation et livraison

Avant publication d’une version utilisable :

- Tests unitaires : conversion des unités, dates aux limites des fenêtres, valeurs facultatives, labels et erreurs de collecte.
- Tests d’intégration sur PostgreSQL 18 : au moins trois utilisateurs dont un sans activité, plusieurs sports pour un même utilisateur et un sport partagé entre utilisateurs ; vérifier les valeurs exactes de chaque couple et les totaux individuels tous sports confondus pour détecter tout mélange d’agrégats.
- Vérifier l’apparition automatique d’un nouvel utilisateur et d’un nouveau code de sport, les activités modifiées ou supprimées, l’absence d’activité et les permissions du rôle dédié.
- Tests HTTP et exposition : scrapes consécutifs et concurrents, panne puis reprise de PostgreSQL, dépassement de délai et schéma incompatible.
- CI : formatage, lint, `go vet`, tests avec détection de courses, compilation, analyse des vulnérabilités Go et de l’image.
- `Dockerfile` multi-stage, images de base versionnées, binaire sous utilisateur non privilégié et `.dockerignore`.
- `docker-compose.yml` pour une base existante et `docker-compose.dev.yml` avec PostgreSQL et données synthétiques ; healthchecks et dépendances cohérents.
- `.env.example` complet avec uniquement des valeurs d’exemple sûres. Aucun secret réel ni dump de données utilisateur dans Git ou dans les fixtures.
- `docker compose config --quiet`, démarrage de la stack de test et test de fumée des endpoints et de la collecte réelle.
- Documenter l’installation des droits, la rotation des secrets, la compatibilité Endurain, le rollback et les procédures de sauvegarde/restauration de l’environnement de test. Ne pas introduire de migration des données Endurain.

## 6. Intégration et preuve runtime

- Ajouter le service à la stack Docker existante et à son réseau Endurain, ainsi qu’au réseau accessible à Prometheus, sans publier PostgreSQL ni les métriques sur Internet.
- Utiliser le gestionnaire de secrets et le flux GitOps existants. Prévoir une authentification du scrape si la frontière réseau ne suffit pas.
- Ajouter un job Prometheus dédié ; commencer par un intervalle de l’ordre de 1 à 5 minutes, à ajuster d’après les mesures SQL.
- Examiner les règles `remote_write` avant le premier scrape : les métriques sportives par utilisateur peuvent être transmises aux destinations distantes déjà configurées.
- Vérifier la révision déployée, les healthchecks, les droits réels et le résultat d’une collecte depuis Prometheus.
- Comparer plusieurs utilisateurs et sports avec Endurain ; valider aussi les données absentes et la reprise après panne sur une stack de test.
- Fournir un dashboard Grafana centré sur l’utilisateur sélectionné, avec ses volumes, sa fréquence, sa dernière activité, des filtres sport/période et la santé de la collecte.
- Valider le rendu des tableaux de bord sur mobile et la distinction entre valeur nulle, zéro et collecte indisponible.

Le pipeline vert ne prouve pas le déploiement : le statut opérationnel nécessite des métriques effectivement scrappées et des données affichées dans Grafana.

## Hors du premier périmètre

Pas d’interface web/PWA, de collecte GPS point par point, de données de sommeil ou de poids, de proxy vers Garmin/Strava, ni de reprise automatique de l’historique dans Prometheus. L’historique complet, s’il est demandé, fera l’objet d’un choix explicite de source et de stockage.
