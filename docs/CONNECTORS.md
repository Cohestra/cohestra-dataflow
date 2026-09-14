# Connectors

DataFlow exposes **25 connector roles: 13 sources and 12 sinks**. The live
catalog is available at `GET /api/connectors/catalog` and is filtered by the
workspace's enabled features.

| 13 sources | 12 sinks |
| --- | --- |
| Zendesk | PostgreSQL |
| Google Sheets | ClickHouse |
| Google Drive | MySQL |
| Microsoft Excel | MongoDB |
| Custom HTTP API | Amazon S3 |
| PostgreSQL | Kafka / Redpanda |
| MySQL | SFTP |
| MongoDB | Snowflake |
| Amazon S3 | Apache Iceberg |
| Kafka / Redpanda | Google Sheets |
| SFTP | Webhook |
| Snowflake | DataFlow managed store |
| Apache Iceberg |  |

## Connect through the application

1. Open **Connectors** and choose a service.
2. Enter its connection details or complete OAuth.
3. Test and save the connection.
4. Select the saved connection from a source or sink node on the pipeline
   canvas.

Credentials are stored separately from pipeline definitions and can be reused
across pipelines.

## Connect through an HTTP client library

Routes accept `Authorization: Bearer <JWT-or-API-token>`, so any HTTP client can
create and reuse connections:

```js
const response = await fetch("https://YOUR_DATAFLOW_HOST/api/connectors", {
  method: "POST",
  headers: {
    "Authorization": `Bearer ${process.env.DATAFLOW_TOKEN}`,
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    provider: "postgres",
    name: "orders-production",
    config: { host: "db.example.com", database: "orders", user: "dataflow" },
    secret: { password: process.env.POSTGRES_PASSWORD },
  }),
});

if (!response.ok) throw new Error(`DataFlow returned ${response.status}`);
const { id: connectionId } = await response.json();
```

Use `GET /api/connectors` to list saved connections. Keep API tokens and
connector secrets in environment variables or a secret manager.

## Administrator-provided REST connectors

Operators can mount declarative REST connector manifests through deployment
configuration. They appear in the same UI and API catalog after the service is
restarted; application users do not edit source code or rebuild images.
Manifests currently support `kind: "source"` only. Sink manifests are excluded
from registration and the catalog because manifest execution has no write
contract. Use a coded sink for writes.
