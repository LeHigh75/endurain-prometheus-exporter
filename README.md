# endurain-prometheus-exporter

Projet d’exporter Prometheus en Go pour les statistiques d’activité de chaque utilisateur d’[Endurain](https://github.com/endurain-project/endurain), avec détail par sport, à visualiser dans Grafana.

**État : première implémentation.** Le collecteur Go, l’authentification Bearer facultative, les droits SQL, Docker et les tests sont fournis. L’intégration à une stack Endurain et à Prometheus reste une étape de déploiement distincte.

## Architecture

```text
PostgreSQL Endurain ← SELECT seul — exporter Go ← scrape — Prometheus ← requêtes — Grafana
```

L’exporter sera un service indépendant : il ne modifiera ni Endurain ni ses données. Un compte PostgreSQL dédié permettra uniquement la lecture des colonnes utiles aux agrégats.

La première cible est **Endurain v0.19.4 avec PostgreSQL 18**. Les requêtes et les droits du compte dédié ont été vérifiés sur une instance de cette version ; les autres versions nécessitent une validation de leur schéma.

## Pourquoi PostgreSQL ?

Endurain possède des [routes de lecture authentifiées](https://github.com/endurain-project/endurain/blob/v0.19.4/backend/app/activities/activity/router.py), utilisées avec des sessions JWT. Dans cette version, les [clés API sont limitées au scope `activities:upload`](https://github.com/endurain-project/endurain/blob/v0.19.4/backend/app/auth/api_keys/utils.py).

Pour les statistiques de tous les utilisateurs, la lecture SQL évite de gérer une session applicative par utilisateur. Elle crée en revanche une dépendance au schéma d’Endurain : chaque mise à jour devra être validée contre les requêtes de l’exporter.

## Premier périmètre proposé

- L’utilisateur est la dimension principale : chacun possède ses propres statistiques et ses totaux tous sports confondus dans Grafana, avec une répartition par sport.
- Un utilisateur peut pratiquer plusieurs sports et un même sport peut être pratiqué par plusieurs utilisateurs.
- Détection automatique des nouveaux utilisateurs et sports présents dans les activités, sans identifiants ni liste de sports codés en dur.
- Nombre d’activités, distance, durée active, dénivelé positif, énergie et date de dernière activité.
- Identifiants utilisateurs sans noms ni emails, y compris pour les comptes sans activité ; exclusion des activités marquées comme masquées, inclusion des activités privées.
- Totaux sur les activités présentes en base et fenêtres glissantes de 7 et 30 jours.
- Santé de la collecte et durée des requêtes.
- Endpoint `/metrics` sans interface web ; protection au choix de l’administrateur : réseau, reverse proxy ou token Bearer facultatif.

Les noms, unités, règles de filtrage et cas limites sont détaillés dans le [plan de réalisation](docs/PLAN.md).

Sans `METRICS_TOKEN` ni `METRICS_TOKEN_FILE`, l’exporter n’applique pas d’authentification HTTP intégrée. Lorsqu’un token est configuré, `/metrics` et `/readyz` exigent `Authorization: Bearer <token>`.

## Démarrage et validation

La stack de développement utilise uniquement des identifiants publics de démonstration et une base PostgreSQL isolée, conservée en mémoire :

```bash
METRICS_TOKEN=development-only-metrics-token-change-me docker compose -f docker-compose.dev.yml up -d --build --wait
METRICS_TOKEN=development-only-metrics-token-change-me python3 tests/smoke.py
```

Pour exécuter aussi les tests unitaires et d’intégration avec Go 1.27.3 :

```bash
TEST_DATABASE_URL='postgresql://endurain_owner:development-only-owner-password@127.0.0.1:55432/endurain_exporter_test?sslmode=disable' go test -count=1 -race -coverprofile=coverage.out ./...
go vet ./...
```

Les tests d’intégration n’acceptent que la base nommée `endurain_exporter_test`. Ils utilisent des données synthétiques et modifient cette base de test. Sans `TEST_DATABASE_URL`, ils sont ignorés ; les autres tests restent exécutés.

L’installation sur une base existante, la configuration, les secrets, les requêtes Grafana et le rollback sont décrits dans [docs/OPERATIONS.md](docs/OPERATIONS.md).

## Images Docker Hub

Le [Dockerfile](Dockerfile) produit une image minimale, sans shell, exécutée sous un utilisateur non privilégié, avec un healthcheck. Il permet de compiler pour `linux/amd64` et `linux/arm64`.

Pour activer la publication, créer le dépôt public `endurain-prometheus-exporter` sur Docker Hub et configurer dans GitHub → Settings → Secrets and variables → Actions :

| Type | Nom | Valeur |
| --- | --- | --- |
| Variable | `DOCKERHUB_USERNAME` | Compte utilisé pour se connecter à Docker Hub |
| Variable | `DOCKERHUB_IMAGE` | `<compte-ou-organisation>/endurain-prometheus-exporter`, sans tag |
| Secret | `DOCKERHUB_TOKEN` | Jeton Docker Hub avec les droits de lecture et d’écriture sur ce dépôt |

Utiliser un [jeton d’accès Docker Hub](https://docs.docker.com/security/access-tokens/) pour la CI. Ne pas le placer dans Git, dans le `.env` applicatif ou dans les discussions.

Après chaque push sur `main`, la CI exécute les tests PostgreSQL, les tests HTTP et les analyses de vulnérabilités avant de publier les deux architectures. Les tags sont `latest` et `sha-<commit-complet>`. La publication est désactivée tant que les deux variables GitHub ne sont pas renseignées ; les pull requests ne publient aucune image. Une première publication peut aussi être lancée depuis Actions → ci → Run workflow, sur `main`.

Pour déployer une image publiée avec le Compose fourni, définir `EXPORTER_IMAGE=<compte-ou-organisation>/endurain-prometheus-exporter:sha-<commit-complet>` dans le fichier d’environnement privé, puis suivre [la procédure Docker](docs/OPERATIONS.md#image-publiée-ou-build-local). L’authentification HTTP par token reste facultative et indépendante de l’accès utilisé pour publier sur Docker Hub.

## Historique et confidentialité

Un scrape expose les valeurs **au moment de la collecte**. Les anciennes activités contribuent immédiatement aux totaux, mais leurs valeurs ne sont pas automatiquement enregistrées dans Prometheus à leurs dates d’origine. Les imports tardifs et suppressions modifient également ces totaux.

Si les tableaux de bord doivent parcourir l’historique complet des activités, prévoir séparément une source PostgreSQL en lecture seule dans Grafana, ou un projet explicite de reprise d’historique. Voir la [documentation de la source PostgreSQL de Grafana](https://grafana.com/docs/grafana/latest/datasources/postgres/).

Un dépôt public ne signifie pas que `/metrics` sera public. Les métriques par utilisateur restent des données personnelles, même avec un identifiant numérique : limiter l’accès à Prometheus et aux tableaux de bord. Vérifier aussi les destinations `remote_write` avant de collecter des données réelles.

## Suite du projet

L’accès PostgreSQL dédié et le collecteur sont implémentés. Il reste à intégrer le service au déploiement cible, configurer son scrape et construire le dashboard par utilisateur. Le besoin de consultation de l’historique complet reste à préciser.

Le détail et les critères de validation figurent dans [docs/PLAN.md](docs/PLAN.md).
