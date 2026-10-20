# endurain-prometheus-exporter

Projet d’exporter Prometheus en Go pour les statistiques d’activité d’[Endurain](https://github.com/endurain-project/endurain), par utilisateur et par sport, à visualiser dans Grafana.

**État : initialisation et conception.** Ce dépôt contient le périmètre et le plan de réalisation ; aucun binaire, image Docker ou exporter opérationnel n’est encore fourni.

## Architecture proposée

```text
PostgreSQL Endurain ← SELECT seul — exporter Go ← scrape — Prometheus ← requêtes — Grafana
```

L’exporter sera un service indépendant : il ne modifiera ni Endurain ni ses données. Un compte PostgreSQL dédié permettra uniquement la lecture des colonnes utiles aux agrégats.

La première cible est **Endurain v0.19.4 avec PostgreSQL 18**. La structure réelle de la base devra être vérifiée avant de déclarer cette combinaison compatible.

## Pourquoi PostgreSQL ?

Endurain possède des [routes de lecture authentifiées](https://github.com/endurain-project/endurain/blob/v0.19.4/backend/app/activities/activity/router.py), utilisées avec des sessions JWT. Dans cette version, les [clés API sont limitées au scope `activities:upload`](https://github.com/endurain-project/endurain/blob/v0.19.4/backend/app/auth/api_keys/utils.py).

Pour les statistiques de tous les utilisateurs, la lecture SQL évite de gérer une session applicative par utilisateur. Elle crée en revanche une dépendance au schéma d’Endurain : chaque mise à jour devra être validée contre les requêtes de l’exporter.

## Premier périmètre proposé

- Statistiques pour chaque utilisateur, ventilées par sport.
- Nombre d’activités, distance, durée active, dénivelé positif, énergie et date de dernière activité.
- Totaux sur les activités présentes en base et fenêtres glissantes de 7 et 30 jours.
- Santé de la collecte et durée des requêtes.
- Endpoint `/metrics` interne, sans interface web.

Les noms, unités, règles de filtrage et cas limites sont détaillés dans le [plan de réalisation](docs/PLAN.md).

## Historique et confidentialité

Un scrape expose les valeurs **au moment de la collecte**. Les anciennes activités contribuent immédiatement aux totaux, mais leurs valeurs ne sont pas automatiquement enregistrées dans Prometheus à leurs dates d’origine. Les imports tardifs et suppressions modifient également ces totaux.

Si les tableaux de bord doivent parcourir l’historique complet des activités, prévoir séparément une source PostgreSQL en lecture seule dans Grafana, ou un projet explicite de reprise d’historique. Voir la [documentation de la source PostgreSQL de Grafana](https://grafana.com/docs/grafana/latest/datasources/postgres/).

Un dépôt public ne signifie pas que `/metrics` sera public. Les métriques par utilisateur restent des données personnelles, même avec un identifiant numérique : limiter l’accès à Prometheus et aux tableaux de bord. Vérifier aussi les destinations `remote_write` avant de collecter des données réelles.

## Prochaines étapes

1. Confirmer le volume de données et le besoin d’historique.
2. Vérifier le schéma réel, les unités et les règles de confidentialité.
3. Préparer les droits PostgreSQL minimaux et les requêtes d’agrégation.
4. Développer et tester le collecteur Go et ses endpoints HTTP.
5. Fournir Docker, Compose, configuration d’exemple et CI.
6. Intégrer le scrape, vérifier les données en runtime, puis construire le dashboard Grafana.

Le détail et les critères de validation figurent dans [docs/PLAN.md](docs/PLAN.md).
