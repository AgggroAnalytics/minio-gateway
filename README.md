# minio-gateway

Serves MinIO objects over HTTP with **CORS `Access-Control-Allow-Origin: *`** so browsers can load shared object URLs (e.g. PMTiles) from any origin.

## Usage

**Env (optional):**

- `MINIO_ENDPOINT` – default `localhost:9000`
- `MINIO_ACCESS_KEY` – default `minioadmin`
- `MINIO_SECRET_KEY` – default `minioadmin`
- `MINIO_USE_SSL` – set to `true` for HTTPS
- `GATEWAY_PORT` – default `8080`

**Request:** `GET /{bucket}/{key}`

Example: `GET /observed-pmtiles/019cfcb4-ae51-757f-ac52-50a035005c7b/2024-06-25.pmtiles` streams that object with CORS headers.

## Run locally

```bash
cd minio-gateway
go run .
# With MinIO on host: MINIO_ENDPOINT=localhost:9000 go run .
```

## Docker / Tilt

- **Docker:** `docker build -t minio-gateway .` then `docker run -p 8080:8080 -e MINIO_ENDPOINT=host.docker.internal:9000 minio-gateway`
- **Tilt:** `minio-gateway` is in `infra/k8s/minio-gateway.yaml` and port-forwarded to **8081**. After `tilt up`, use `http://localhost:8081/{bucket}/{key}` for CORS-free access (e.g. `http://localhost:8081/observed-pmtiles/field-id/2024-06-25.pmtiles`).
