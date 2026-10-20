# Installation et exploitation

## Compte PostgreSQL

La base applicative doit déjà exister. Depuis le checkout, exécuter le fichier de droits avec le compte administrateur existant du conteneur PostgreSQL :

```bash
docker exec -i <conteneur-postgresql> sh -c \
  'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
  < sql/grants.sql
```

Le rôle `endurain_exporter` est réservé à cet exporter. Le script n’accorde que `CONNECT`, `USAGE` sur `public`, la lecture de `users.id` et de huit colonnes d’`activities`. Il ne modifie pas les données, les droits de l’application ni les privilèges de `PUBLIC`. Auditer les permissions effectives si le rôle existait déjà ou si `PUBLIC` dispose de droits supplémentaires dans cette base.

Définir ensuite son mot de passe avec l’invite interactive de PostgreSQL :

```bash
docker exec -it <conteneur-postgresql> sh -c \
  'exec psql -X -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "\password endurain_exporter"'
```

Le mot de passe n’est ainsi pas placé dans la ligne de commande. Utiliser un mot de passe aléatoire indépendant et le conserver dans le gestionnaire de secrets. Le rôle dispose d’au plus trois connexions et démarre avec des transactions en lecture seule, des requêtes limitées à cinq secondes et une attente de verrou limitée à une seconde. Les permissions empêchent les écritures même si une connexion désactive son mode lecture seule.

Le collecteur utilise un pool de deux connexions, des transactions `REPEATABLE READ READ ONLY` et les deux requêtes d’un même snapshot. Aucune migration ou création de schéma n’est exécutée par le binaire.

## Configuration et secrets

Toutes les variables figurent dans [`.env.example`](../.env.example). Seule la connexion PostgreSQL est obligatoire ; les autres paramètres sont facultatifs :

| Variable | Usage |
| --- | --- |
| `DATABASE_URL` ou `DATABASE_URL_FILE` | Connexion avec le rôle dédié ; fichier contenant uniquement l’URL si le mode fichier est choisi |
| `METRICS_TOKEN` ou `METRICS_TOKEN_FILE` | Authentification Bearer facultative ; token de 32 à 4 096 caractères ASCII imprimables sans espace, directement ou dans un fichier |
| `LISTEN_ADDRESS` | Adresse HTTP, `:9808` par défaut |
| `SCRAPE_TIMEOUT` | Budget global de collecte, `5s` par défaut ; de `1ms` à `1m` |
| `MAX_SERIES` | Budget de séries métier, `20000` par défaut ; de `1` à `1000000` |

Ne pas définir simultanément la variable directe et sa variante `_FILE`. Sans token, l’exporter délègue le contrôle d’accès au réseau ou au reverse proxy choisi par l’administrateur. Avec un token, `/metrics` et `/readyz` exigent un en-tête Bearer valide ; le token n’est jamais accepté dans l’URL. Un fichier secret explicitement configuré doit être lisible et non vide : une erreur de fichier arrête le démarrage. Les secrets sont chargés au démarrage : recréer ou redémarrer l’exporter après rotation.

Avec un agent OpenBao qui exporte des champs normalisés dans un `.env`, les champs `database.url` et `metrics.token` deviennent `DATABASE_URL` et `METRICS_TOKEN`. Préserver les autres champs avec un CAS lors des mises à jour KV v2. Vérifier la relecture dans Bao et le fichier rendu sans afficher leurs valeurs.

Le Compose fourni consomme les variables directes, par exemple depuis un fichier rendu par l’agent. Il transmet aussi `METRICS_TOKEN_FILE` si cette variable est définie ; fournir alors le montage en lecture seule correspondant dans la stack.

## Image publiée ou build local

Pour utiliser une image Docker Hub, définir `EXPORTER_IMAGE=<compte-ou-organisation>/endurain-prometheus-exporter:sha-<commit-complet>` dans le fichier d’environnement privé. Le tag correspondant à un commit ou, mieux, le digest identifie précisément l’image à déployer et à conserver pour un rollback. `latest` suit la dernière publication réussie de `main`.

Depuis le répertoire contenant le Compose :

```bash
docker compose --env-file /chemin/prive/exporter.env config --quiet
docker compose --env-file /chemin/prive/exporter.env up -d --no-build --pull always --wait
```

Pour construire localement depuis le code source, conserver la valeur par défaut `EXPORTER_IMAGE=endurain-prometheus-exporter:local` :

```bash
docker compose --env-file /chemin/prive/exporter.env up -d --build --wait
```

Définir aussi `ENDURAIN_NETWORK` avec le nom du réseau Docker de PostgreSQL. Le port HTTP est publié sur `127.0.0.1` par défaut. Prometheus doit partager un réseau avec l’exporter pour le joindre directement. L’administrateur choisit la protection HTTP adaptée à son installation, par exemple un réseau privé, un reverse proxy HTTPS ou le Bearer intégré.

## Endpoints et scrape

- `GET /metrics` : accès direct si aucun token n’est configuré. Avec un token configuré, un Bearer absent ou incorrect donne `401`, sans requête SQL. La réponse utilise `Cache-Control: no-store`.
- `GET /healthz` : `200` lorsque le processus HTTP fonctionne ; n’interroge pas PostgreSQL.
- `GET /readyz` : même mode d’authentification que `/metrics` ; `200` si la collecte complète est disponible, `503` en cas de panne, schéma incompatible ou budget dépassé.
- Une collecte en échec répond sur `/metrics` avec `endurain_up 0` et la durée de collecte, sans métriques métier anciennes, partielles ou mises à zéro.

Exemple Prometheus lorsque le Bearer intégré est activé :

```yaml
scrape_configs:
  - job_name: endurain
    scrape_interval: 60s
    scrape_timeout: 10s
    authorization:
      type: Bearer
      credentials_file: /run/secrets/endurain_metrics_token
    static_configs:
      - targets: [endurain-exporter:9808]
```

Le fichier d’identifiants doit contenir le token brut, sans préfixe `Bearer`, avec des permissions adaptées au compte Prometheus. Sans authentification intégrée, retirer le bloc `authorization` ou l’adapter à la protection externe choisie. Examiner les destinations `remote_write` avant le premier scrape de données réelles.

La stack de développement utilise un token de démonstration par défaut. Pour tester le mode sans authentification intégrée :

```bash
METRICS_TOKEN= docker compose -f docker-compose.dev.yml up -d --wait exporter
METRICS_TOKEN= python3 tests/smoke.py
```

## Données par utilisateur dans Grafana

Le label `user_id` identifie toujours l’utilisateur. `sport_id` reprend le code numérique Endurain, sans liste figée de sports. Les activités privées sont incluses ; `is_hidden = true` est exclu. Tous les comptes sont inventoriés via `endurain_user_info`, y compris sans activité ou désactivés. Aucun nom, email, titre ou parcours n’est exporté.

Pour les comptes sans activité, aucune combinaison de sport artificielle n’est créée. Le nombre d’activités peut être affiché à zéro avec :

```promql
sum by (user_id) (endurain_activities{period="all"})
or on (user_id) (0 * endurain_user_info)
```

Pour la distance et la durée, sommer les sports d’un utilisateur après avoir sélectionné une seule période. Pour la dernière activité, utiliser `max by (user_id)`.

Le dénivelé et l’énergie sont omis pour un sport/période dès qu’une mesure manque. Pour afficher un total individuel exact, vérifier que tous les sports actifs de la période disposent de la mesure. Exemple pour l’énergie :

```promql
sum by (user_id) (endurain_activity_energy_joules{period="all"})
and on (user_id) (
  count by (user_id) (endurain_activity_energy_joules{period="all"})
  ==
  count by (user_id) (endurain_activities{period="all"} > 0)
)
```

Les fenêtres `7d` et `30d` sont glissantes. Les gauges reflètent les données actuellement présentes, y compris les modifications et suppressions : ne pas leur appliquer `rate()` ou `increase()`. Un scrape ne reconstitue pas l’historique aux dates des anciennes activités.

## Rotation, rollback et sauvegardes

Pour activer ou changer le token, mettre à jour son secret, redémarrer l’exporter et actualiser le fichier de Prometheus. Vérifier ensuite `401` sans token et une collecte avec `endurain_up 1` avec le nouveau token. Pour déléguer l’accès à une protection externe, retirer `METRICS_TOKEN` et `METRICS_TOKEN_FILE`, redémarrer l’exporter et adapter le scrape.

Pour revenir à une version précédente, conserver son image identifiée par digest, la redéployer puis vérifier l’authentification, `/readyz` et un scrape. L’exporter ne conserve aucun état et n’effectue aucune migration ; son retrait ne supprime pas les données Endurain.

Pour désactiver l’accès SQL, arrêter l’exporter puis exécuter `ALTER ROLE endurain_exporter NOLOGIN` avec l’administrateur PostgreSQL. Cela conserve les permissions et le mot de passe pour une remise en service contrôlée. Une révocation immédiate nécessite aussi de terminer les éventuelles connexions de ce rôle. La suppression définitive du rôle doit être précédée d’un inventaire de ses permissions et dépendances.

Les données Endurain restent couvertes par la procédure de sauvegarde/restauration de l’application. Les secrets et le rôle dédié doivent être conservés dans les sauvegardes appropriées du gestionnaire de secrets et des rôles PostgreSQL. Les données de la stack de développement sont synthétiques, en mémoire et perdues à l’arrêt : elles se recréent depuis `tests/schema.sql` et les tests d’intégration.
