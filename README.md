# Delivery Service

Go-сервис регистрации международных посылок, расчёта доставки, назначения перевозчика и аналитики по дням. Есть Web UI, JSON API и Swagger. Данные публичного API изолированы по `session_id` cookie; это сессия браузера, а не система аутентификации.

## Запуск

Нужны Docker и Docker Compose. Для новой установки:

```sh
cp .env.example .env.docker
docker compose --env-file .env.docker -f docker-compose/docker-compose.yml up -d --build
```

Если `.env.docker` уже настроен, не перезаписывайте его. `.env.example` содержит только локальные development credentials и адреса сервисов внутри Compose.

- Web UI: http://localhost:8080/
- Readiness: http://localhost:8080/health
- Swagger: http://localhost:8080/swagger/index.html
- OpenAPI JSON: http://localhost:8080/swagger/doc.json
- RabbitMQ management: http://localhost:15672/ — development login из `RABBITMQ_DEFAULT_USER` / `RABBITMQ_DEFAULT_PASS`.

```sh
docker compose --env-file .env.docker -f docker-compose/docker-compose.yml ps -a
docker compose --env-file .env.docker -f docker-compose/docker-compose.yml logs -f app
```

Compose ждёт healthy PostgreSQL, Redis, MongoDB, RabbitMQ и успешного завершения migrate. Миграции выполняются отдельным одноразовым контейнером. Volumes сохраняют данные при перезапуске. Не используйте `down -v`, если данные нужны.

Существующий volume должен уже иметь согласованную историю `schema_migrations`. Для схемы, ранее созданной вручную, сначала сделайте backup и сверку схемы с миграциями; не выполняйте автоматический `force` и не удаляйте volume. Новая пустая БД создаётся миграциями автоматически.

## Архитектура

```mermaid
flowchart LR
  UI[Web UI / HTTP] --> H[Handler: validator, session, trace]
  H --> S[Service]
  S -->|тип посылки| PG[(PostgreSQL)]
  S -->|persistent + mandatory + confirm| MQ[RabbitMQ]
  MQ --> C[Consumer: prefetch 1]
  C --> W[WorkerService]
  W --> R[Курс: Redis / CBR / last known]
  W --> PG
  W --> M[(MongoDB calculation_logs)]
  W -->|успех обоих хранилищ| A[Manual ACK]
```

Слои: handler → service → repository; зависимости собираются в `internal/app`. HTML/CSS/JS встроены в Go-бинарник через `embed`, поэтому UI работает с того же origin и cookie без CORS и отдельного frontend-сервера.

### Регистрация и расчёт

`POST /api/v1/packages` валидирует запрос через validator/v10, проверяет тип в PostgreSQL, создаёт UUID v4 и единый `created_at`, переносит trace ID текущего запроса и публикует сообщение в `packages.to_calculate`. Ответ `201 registered` приходит только после успешных routing и publisher confirm. POST **не вставляет посылку в PostgreSQL**.

Worker рассчитывает:

```text
delivery_cost_rub = Round((weight_kg × 0.5 + content_value_usd × 0.01) × usd_to_rub_rate, 2)
```

Decimal arithmetic сохраняет точность расчёта. Redis хранит курс один час и отдельно последний успешный курс без TTL. При недоступности CBR используется last known; если курса нет, сообщение повторяется через retry queue.

Регистрация асинхронная: сразу после `201` GET может временно вернуть `404`. UI показывает принятие в обработку и делает не более 12 попыток polling с интервалом 1,5 секунды, затем предлагает обновить список вручную.

### Idempotency-Key

POST поддерживает optional header `Idempotency-Key` (одно значение, 1..128 printable ASCII characters). Одинаковые session + key + нормализованный request возвращают **тот же package_id и created_at**. Порядок JSON-полей и эквивалентная запись decimals не меняют hash. Другой payload с тем же ключом — `409 IDEMPOTENCY_KEY_CONFLICT`; невалидный ключ — `400 IDEMPOTENCY_KEY_INVALID`. Без header сохраняется прежнее поведение: каждый POST — новая регистрация.

Migration 3 создаёт `package_idempotency`: UUID session/package, ключ VARCHAR(128), SHA-256 hash BYTEA и TIMESTAMPTZ. Резервация фиксируется до RabbitMQ publish; открытая PostgreSQL transaction во время ожидания confirm отсутствует. Повтор, в том числе после publish failure/потери ответа, использует ту же резервацию и может повторно опубликовать тот же package_id. Worker устраняет логические дубликаты. Ключи не удаляются автоматически: гарантия сохраняется, пока существует metadata записи.

UI генерирует ключ через `crypto.randomUUID()`, сохраняет key/payload в sessionStorage для повтора после сетевой ошибки и сбрасывает после 201. Следующая новая регистрация получает новый key. API list/detail показывает сохранённый при расчёте `usd_to_rub_rate` как nullable точную строку; UI выводит его в колонке «Курс USD/RUB», для pending — «—». GET не получает курс заново.

### RabbitMQ и идемпотентность

Все три очереди durable; сообщения persistent:

- `packages.to_calculate` — основная очередь;
- `packages.to_calculate.retry` — задержка 5 секунд через TTL и dead letter обратно в основную;
- `packages.to_calculate.dlq` — ошибочные сообщения и исчерпанные retries.

Consumer использует prefetch=1 и manual ACK. ACK следует после PostgreSQL и MongoDB либо после подтверждённого переноса в retry/DLQ. По умолчанию допускаются 3 retries после исходной попытки. При ошибке переноса исходное сообщение остаётся unacked. DLQ автоматически не переигрывается: после исправления причины сообщение можно вручную повторно опубликовать в основную очередь.

PostgreSQL сохраняет канонический результат по UUID и не перезаписывает рассчитанную посылку. MongoDB использует уникальный `_id = package_id` и `$setOnInsert`. Существующий лог сверяется с canonical business fields: UUID/session/type ID, точными decimals, created/calculated timestamp с точностью миллисекунд MongoDB Date. Несовпадение возвращает `ErrCalculationLogConflict`, лог не перезаписывается; consumer отправляет сообщение в DLQ и логирует поле несовпадения. Trace ID относится к попытке/recovery, а type name читается из изменяемого справочника: они не сравниваются, в логе сохраняются первоначальные значения. При сбое MongoDB повторная доставка только дописывает лог, сохраняя первоначальные cost/rate/calculated_at. Это at-least-once доставка с идемпотентной обработкой, а не общая транзакция двух БД.

Consumer не делает reconnect. Его неожиданная fatal-ошибка делает приложение неготовым и инициирует shutdown; после восстановления брокера перезапустите app. При штатном shutdown прекращается приём HTTP и новых обработок, текущая обработка получает `HTTP_SHUTDOWN_TIMEOUT`; незавершённое остаётся для redelivery. `/health` проверяет PostgreSQL, Redis, MongoDB, RabbitMQ connection и consumer, возвращая 503 при неготовности.

Scheduler отсутствует. При `DEBUG_MODE=true` остаётся `POST /api/v1/admin/calculate-pending`: ручной recovery старых pending-записей через тот же WorkerService. Он также повторяет Mongo upsert готовых записей с полными метаданными без пересчёта; legacy calculated без сохранённого курса/времени не реконструируются.

### Перевозчик и аналитика

`POST /api/v1/packages/{package_id}/assign-carrier` принимает `{"carrier_id":123}`. Атомарный `UPDATE ... WHERE carrier_id IS NULL AND session_id=... RETURNING carrier_id` реализует optimistic locking: первая попытка выигрывает. Повторное назначение, включая тот же ID, возвращает 409. Чужая или отсутствующая посылка — 404. Проверка после неуспешного UPDATE нужна только для различения 404/409.

`GET /api/v1/analytics/daily-sum?date=2026-10-11` требует дату `YYYY-MM-DD`, считает день в UTC и только текущую session. Mongo Aggregation Pipeline выполняет `$match` по session и диапазону `[00:00, next day)`, `$group` по type ID, `$sum` Decimal128 и количества, `$sort` по type ID. Имя берётся из последнего лога дня; переименование не создаёт дополнительной группы. Суммы JSON — точные строки, пустой день — `data: []`. Используется индекс `{session_id:1, calculated_at:1}`.

## API

| Метод | Путь | Назначение |
|---|---|---|
| POST | `/api/v1/packages` | Асинхронная регистрация, 201 |
| GET | `/api/v1/package-types` | Типы посылок |
| GET | `/api/v1/packages` | Список: `page`, `limit`, `package_type_id`, `calculated` |
| GET | `/api/v1/packages/{package_id}` | Детали своей посылки |
| POST | `/api/v1/packages/{package_id}/assign-carrier` | Первое назначение перевозчика |
| GET | `/api/v1/analytics/daily-sum?date=YYYY-MM-DD` | Суммы текущей session за день UTC |
| POST | `/api/v1/admin/calculate-pending` | Ручной recovery, только DEBUG_MODE |
| GET | `/health` | Readiness пяти компонентов |

Сохраняйте cookie между запросами:

```sh
curl -c /tmp/delivery-cookie -b /tmp/delivery-cookie \
  -H 'Content-Type: application/json' \
  -d '{"name":"Ноутбук","weight_kg":2,"package_type_id":2,"content_value_usd":100}' \
  http://localhost:8080/api/v1/packages
curl -b /tmp/delivery-cookie 'http://localhost:8080/api/v1/packages?page=1&limit=10'
curl -b /tmp/delivery-cookie 'http://localhost:8080/api/v1/analytics/daily-sum?date=2026-10-11'
```

Все API-ошибки имеют формат `{"error":{"code":"...","message":"...","details":{}}}`. UI отображает message и validation details. Журналы приложения — JSON slog с trace ID и duration HTTP-запроса.

## Разработка и проверки

Нужен Go версии из `go.mod`. Для локального процесса используйте `.env.local`: PostgreSQL `localhost:5437`, Redis `localhost:6380`, MongoDB `localhost:27017`, RabbitMQ `localhost:5672`. Приложение читает environment процесса, не загружает `.env` само:

```sh
set -a
. ./.env.local
set +a
go run ./cmd/api
```

```sh
gofmt -w .
go test ./...
go vet ./...
go test -race ./...
golangci-lint run ./...
swag init -g cmd/api/main.go
```

Интеграции работают с поднятыми зависимостями; PostgreSQL использует отдельную тестовую schema, MongoDB — отдельную тестовую БД, RabbitMQ — отдельные тестовые очереди. Development credentials ниже соответствуют `.env.example`:

```sh
TEST_DATABASE_URL='postgres://delivery:delivery@localhost:5437/delivery?sslmode=disable' \
  go test -tags=integration ./internal/repository -run 'Package|Recovery|AssignCarrier|Idempotency' -v
TEST_MONGO_URI='mongodb://delivery:delivery@localhost:27017/delivery?authSource=admin' \
  go test -tags=integration ./internal/repository -run 'Mongo' -v
TEST_RABBITMQ_URL='amqp://delivery:delivery@localhost:5672/' \
  go test -tags=integration ./internal/messaging ./internal/worker -v
```

Добавьте `-race` к интеграционным командам для проверки конкурентных сценариев. Основные Go API документированы комментариями Go Doc. Итоговое сопоставление с ТЗ: [docs/FINAL_AUDIT.md](docs/FINAL_AUDIT.md).

## Конфигурация

Все настройки перечислены в `.env.example`: подключения четырёх зависимостей, Compose credentials, `CBR_URL`, `DEBUG_MODE`, `COOKIE_SECURE`, `HTTP_ADDR` и HTTP timeouts. `CALCULATION_INTERVAL` удалён. При HTTPS установите `COOKIE_SECURE=true`; для локального HTTP — false. Development credentials не предназначены для production.
