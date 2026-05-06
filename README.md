# P2P File Share (Go + Fiber v3 + PostgreSQL)

Простой p2p-файлообменник с веб-интерфейсом, tracker-сервисом и peer-сервисами.

## Что реализовано

- `tracker`:
  - хранит активные пиры;
  - хранит карту `файл -> чанки -> пиры`;
  - удаляет неактивные пиры по TTL.
- `peer`:
  - режет файлы на чанки и раздаёт их;
  - скачивает чанки у других пиров и собирает файл;
  - имеет web UI.
- авторизация/регистрация:
  - PostgreSQL;
  - пользователи и сессии (token);
  - защищённые действия: upload/download, просмотр локальных/сетевых файлов, история действий.

## Запуск одной командой

```bash
docker compose up --build -d
```

Поднимутся:
- `postgres` (auth data)
- `tracker` (`:7000`)
- `peer` (`:8080`, web UI)
- `peer2` (`:8081`, web UI)

## Как работает p2p в этом коде

1. Peer регистрируется на tracker (`peer_id`, `peer_base_url`).
2. При upload peer:
   - делит файл на чанки;
   - сохраняет чанки и метаданные;
   - делает announce на tracker, какие чанки у него есть.
3. При download peer:
   - спрашивает у tracker список пиров для каждого чанка;
   - скачивает чанки напрямую у пиров через `/p2p/chunks/:fileID/:idx`;
   - проверяет SHA-256 каждого чанка;
   - собирает файл.

## Почему количество пиров может не увеличиваться

Tracker учитывает пиры по `peer_id`.  
Если разные устройства запускаются с одинаковым `PEER_ID`, они перезаписывают друг друга в tracker и визуально кажется, что пир всегда один.

Что важно:
- у каждого устройства/инстанса должен быть **уникальный** `PEER_ID`;
- `PUBLIC_BASE_URL` должен быть реальным адресом, по которому к этому peer могут подключиться другие пиры.

В текущем коде, если `PEER_ID` не задан, он автоматически формируется как `<hostname>-<port>`, что снижает риск коллизий.

## ENV переменные

Файл: `.env`

- `TRACKER_TOKEN` — токен доступа к tracker API.
- `TRACKER_PORT`, `PEER_PORT`, `PEER2_PORT` — порты.
- `PEER_ID`, `PEER2_ID` — идентификаторы пиров.
- `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_PORT` — PostgreSQL.
- `SESSION_TTL` — TTL сессии пользователя.
- `CHUNK_SIZE_BYTES` — размер чанка.
- `HEARTBEAT_PERIOD` — heartbeat peer -> tracker.
- `TRACKER_CLEANUP_TTL`, `TRACKER_CLEANUP_TICK` — очистка неактивных пиров.

## REST API

## Tracker API

Base URL: `http://localhost:7000`  
Требует заголовок `X-API-Token: <TRACKER_TOKEN>` для `/api/v1/*`.

- `GET /healthz` — проверка tracker.
- `POST /api/v1/peers/register` — регистрация пира.
- `POST /api/v1/peers/heartbeat` — heartbeat.
- `POST /api/v1/files/announce` — announce файла.
- `GET /api/v1/files` — список файлов в сети.
- `GET /api/v1/files/:id` — детали файла и пиры по чанкам.

## Peer API (auth)

Base URL: `http://localhost:8080` или `http://localhost:8081`

Публичные:
- `GET /healthz`
- `GET /`
- `POST /api/v1/auth/register`
- `POST /api/v1/auth/login`

Защищённые (`Authorization: Bearer <token>`):
- `GET /api/v1/auth/me`
- `POST /api/v1/auth/logout`
- `GET /api/v1/files/local`
- `GET /api/v1/files/network`
- `POST /api/v1/upload` (multipart, поле `file`)
- `POST /api/v1/download/:id`
- `GET /api/v1/me/actions` — история upload/download текущего пользователя.

Внутренний p2p endpoint:
- `GET /p2p/chunks/:fileID/:idx`

## Как тестировать через Postman

1. Запустить сервисы:
```bash
docker compose up --build -d
```

2. Register:
- `POST http://localhost:8080/api/v1/auth/register`
- Body JSON:
```json
{
  "username": "alice",
  "password": "alice123"
}
```

3. Login:
- `POST http://localhost:8080/api/v1/auth/login`
- Скопировать `token` из ответа.

4. Upload:
- `POST http://localhost:8080/api/v1/upload`
- Headers: `Authorization: Bearer <token>`
- Body: `form-data`, key=`file`, type=`File`.

5. Получить файл из сети:
- `GET http://localhost:8081/api/v1/files/network`
- Headers: `Authorization: Bearer <token второго пользователя>`
- взять `id`.

6. Download:
- `POST http://localhost:8081/api/v1/download/<id>`
- Headers: `Authorization: Bearer <token>`

## Примечания по деплою

- Для реального multi-device использования вынеси `PUBLIC_BASE_URL` каждого peer на публичный IP/домен.
- На проде обязательно замени:
  - `TRACKER_TOKEN`
  - `POSTGRES_PASSWORD`
- `.env` уже исключён в `.gitignore`.
