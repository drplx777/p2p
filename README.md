# P2P File Share (BitTorrent-like) on Go + Fiber v3

Учебный p2p-файлообменник, вдохновленный BitTorrent:
- есть `tracker` (реестр пиров и доступных чанков);
- есть `peer`-ноды, которые режут файл на чанки, раздают чанки и скачивают их у других пиров;
- есть веб-интерфейс для загрузки/скачивания;
- запуск полностью готов через `docker-compose up`.

## Технологии

- Go
- Fiber v3 (REST API)
- Docker + Docker Compose

## Как работает код

### 1) Роли сервисов

- `tracker`:
  - хранит активные пиры;
  - хранит метаданные файлов и карту `chunk -> peers`;
  - отдает список файлов и детали по конкретному файлу;
  - удаляет неактивных пиров по TTL.

- `peer`:
  - принимает файл (upload), режет на чанки фиксированного размера;
  - сохраняет чанки на диск;
  - считает хэши чанков и хэш файла (file ID);
  - анонсирует трекеру, что у него есть файл;
  - при скачивании берет у трекера список пиров по каждому чанку и качает чанки напрямую у пиров;
  - проверяет SHA-256 каждого чанка;
  - собирает файл и сохраняет в `data/downloads`.

### 2) Поток upload

1. Клиент отправляет файл на peer (`POST /api/v1/upload`).
2. Peer режет файл на чанки по `CHUNK_SIZE_BYTES`.
3. Peer сохраняет:
   - чанки в `data/chunks/<file_id>/<index>.chk`;
   - метаданные в `data/meta/<file_id>.json`.
4. Peer отправляет announce на tracker (`POST /api/v1/files/announce`).

### 3) Поток download

1. Клиент вызывает download на peer (`POST /api/v1/download/:id`).
2. Peer запрашивает у tracker детали файла (`GET /api/v1/files/:id`).
3. Для каждого чанка peer выбирает доступные URL пиров и пытается скачать чанк.
4. После скачивания проверяется SHA-256 чанка.
5. Все чанки собираются в исходный файл, файл сохраняется в `data/downloads`.
6. Новый peer также анонсирует файл на tracker.

## Структура проекта

- `cmd/app/main.go` — точка входа, выбор режима (`tracker` / `peer`).
- `internal/config` — загрузка env-конфига.
- `internal/models` — DTO/модели для API.
- `internal/tracker` — in-memory хранилище, API tracker и cleanup.
- `internal/peer` — peer API, storage, клиент tracker, downloader.
- `Dockerfile`, `docker-compose.yml` — деплой.

## Быстрый запуск (deploy-ready)

### Локально/на сервере

1. Убедись, что установлены Docker и Docker Compose.
2. В корне проекта выполните:

```bash
docker-compose up --build
```

После запуска:
- tracker: `http://localhost:7000`
- peer #1 UI: `http://localhost:8080`
- peer #2 UI: `http://localhost:8081`

Таким образом сразу есть минимум 2 peer-ноды для p2p-обмена.

## Переменные окружения

Основной env-файл: `.env` (секреты и runtime-конфиг).

Ключевые переменные:
- `TRACKER_TOKEN` — общий секрет для доступа к tracker API;
- `TRACKER_PORT`, `PEER_PORT`, `PEER2_PORT` — порты сервисов;
- `PEER_ID`, `PEER2_ID` — идентификаторы нод;
- `CHUNK_SIZE_BYTES` — размер чанка;
- `HEARTBEAT_PERIOD` — период heartbeat на tracker;
- `TRACKER_CLEANUP_TTL`, `TRACKER_CLEANUP_TICK` — очистка неактивных пиров.

## REST API (Fiber v3)

Все tracker-эндпоинты требуют заголовок:

`X-API-Token: <TRACKER_TOKEN>`

### Tracker API

Базовый URL: `http://localhost:7000`

1) `GET /healthz`  
Проверка состояния tracker.

2) `POST /api/v1/peers/register`  
Регистрация пира.

Body:
```json
{
  "peer_id": "peer-prod-01",
  "peer_base_url": "http://peer:8080"
}
```

3) `POST /api/v1/peers/heartbeat`  
Heartbeat активного пира (тот же body, что register).

4) `POST /api/v1/files/announce`  
Анонс файла и доступных чанков.

Body:
```json
{
  "peer_id": "peer-prod-01",
  "peer_base_url": "http://peer:8080",
  "file": {
    "id": "file_hash",
    "name": "movie.mp4",
    "size_bytes": 12345,
    "chunk_size": 524288,
    "chunk_count": 2,
    "chunk_hashes": ["hash1", "hash2"]
  },
  "available_chunks": [0, 1]
}
```

5) `GET /api/v1/files`  
Список файлов в сети.

6) `GET /api/v1/files/:id`  
Детали файла + карта `chunk_to_peers`.

### Peer API

Peer #1 базовый URL: `http://localhost:8080`  
Peer #2 базовый URL: `http://localhost:8081`

1) `GET /healthz`  
Проверка состояния peer.

2) `GET /`  
Веб-интерфейс peer.

3) `GET /api/v1/files/local`  
Локально доступные файлы на конкретном peer.

4) `GET /api/v1/files/network`  
Файлы из tracker.

5) `POST /api/v1/upload`  
Upload файла на peer (multipart/form-data, поле `file`).

6) `POST /api/v1/download/:id`  
Скачать файл по `file_id` через p2p-чанки.

7) `GET /p2p/chunks/:fileID/:idx`  
Внутренний p2p-эндпоинт: выдача конкретного чанка.

## Как протестировать через Postman

### Подготовка

1. Запусти сервисы:
```bash
docker-compose up --build
```

2. В Postman создай переменные окружения:
- `tracker_url = http://localhost:7000`
- `peer1_url = http://localhost:8080`
- `peer2_url = http://localhost:8081`
- `api_token = (значение TRACKER_TOKEN из .env)`

### Сценарий проверки p2p

1) Проверка health:
- `GET {{tracker_url}}/healthz`
- `GET {{peer1_url}}/healthz`
- `GET {{peer2_url}}/healthz`

2) Загрузка файла в peer1:
- `POST {{peer1_url}}/api/v1/upload`
- Body: `form-data`
  - key: `file`, type: `File`, выбрать любой файл.

3) Получение списка файлов из сети:
- `GET {{peer2_url}}/api/v1/files/network`
- Скопировать `id` файла.

4) Скачивание файла на peer2:
- `POST {{peer2_url}}/api/v1/download/<file_id>`

5) Проверка, что файл появился локально на peer2:
- `GET {{peer2_url}}/api/v1/files/local`

Если пункты 2-5 проходят, p2p-обмен работает: peer2 получил файл чанками от peer1.

## Ручной запуск без Docker

```bash
go mod tidy
go build ./...
```

Запуск tracker:
```bash
APP_MODE=tracker PORT=7000 TRACKER_TOKEN=your_token go run ./cmd/app
```

Запуск peer:
```bash
APP_MODE=peer PORT=8080 PEER_ID=peer-local TRACKER_URL=http://localhost:7000 PUBLIC_BASE_URL=http://localhost:8080 TRACKER_TOKEN=your_token go run ./cmd/app
```

## Безопасность и git

- Секреты хранятся в `.env`.
- `.env` уже добавлен в `.gitignore`, чтобы не утекали секреты в GitHub.
- Также в `.gitignore` добавлены runtime-данные (`data/`) и бинарники.
