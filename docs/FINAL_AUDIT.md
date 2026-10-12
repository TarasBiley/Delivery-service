# Финальный аудит delivery-service

Дата: 2026-10-11. Основа: полное ТЗ из архитектурного запроса и требования Этапа 5. Проверен финальный вариант со всеми дополнительными заданиями. RabbitMQ-задание заменяет два положения базовой версии: прямой INSERT из POST и периодический scheduler. Они намеренно отсутствуют в основном workflow.

## Обязательная часть

| Требование | Статус | Где реализовано | Как проверено |
|---|---|---|---|
| Go, handler → service → repository, DI | Выполнено | `internal/app`, `internal/handler`, `internal/service`, `internal/repository` | Unit/vet/lint, startup Compose |
| POST /api/v1/packages | Выполнено | `handler/package.go`, `service/package.go` | Handler/service tests, browser E2E: 201 |
| validator/v10, name/weight/type/content validation | Выполнено | `model/package.go`, `validation/decimal.go`, `handler/package.go` | Validation и handler tests |
| UUID v4, session_id, единый created_at | Выполнено | `service/package.go`, `middleware/session.go` | Create tests, E2E |
| Ответ package_id/status registered/created_at | Выполнено | `model/CreatePackageResponse` | Unit, browser E2E |
| INSERT pending из POST | Заменено дополнительным RabbitMQ-заданием | `service/PackageService.Create` публикует; worker сохраняет calculated | Service interface не содержит Create; тест запрещает INSERT |
| GET package-types | Выполнено | `handler/package_type.go`, `service/package_type.go` | Unit, browser загрузка select |
| GET packages, pagination | Выполнено | `handler/package.go`, `repository/package.go`, `web/app.js` | PostgreSQL integration; browser page 1→2→1 по контрактным fixtures |
| Фильтры package_type_id, calculated | Выполнено | `handler/package.go`, `repository/package.go` | Handler/PostgreSQL tests; browser filter |
| Только текущая session | Выполнено | PostgreSQL predicates, Mongo `$match`, middleware cookie | Integration, browser foreign session |
| GET package by UUID; чужая/отсутствующая → 404 | Выполнено | `handler/package.go`, `service/package.go`, `repository/package.go` | Unit/integration/E2E foreign GET: 404 |
| PostgreSQL schema и автоматические migrations | Выполнено | `migrations/1_init.*`, `2_package_metadata.*`, `3_package_idempotency.*`, Compose migrate | Integration из пустой тестовой schema, Compose migrate exit 0 |
| Расчёт каждые 5 минут, batch 100, SKIP LOCKED | Заменено дополнительным RabbitMQ-заданием | Consumer prefetch 1 + WorkerService; scheduler удалён | Rabbit integration, отсутствие scheduler/CALCULATION_INTERVAL; legacy SQL helpers тестируются, но автоматически не запускаются |
| Формула (weight×0.5 + content×0.01)×rate, Round(2) | Выполнено | `service/calculator.go` | Calculator tests, E2E: 170.96 |
| status calculated | Выполнено | `service/worker.go`, `repository/package_worker.go` | PostgreSQL integration/E2E |
| Redis TTL курса 1 час | Выполнено | `cache/exchange_rate.go` | Cache/service tests и проверка реализации |
| Fallback last known | Выполнено | `service/exchange_rate.go`, `cache/exchange_rate.go` | Unit; реальный E2E использовал last known при timeout CBR |
| DEBUG calculate-pending | Выполнено как ручной recovery | `service/calculation.go`, `handler/admin.go`, debug router | Recovery tests; router test проверяет отсутствие endpoint при DEBUG=false |
| JSON slog, trace_id, HTTP duration | Выполнено | `middleware/logging.go`, `tracing/context.go`, worker | Логи E2E; trace совпадает в Mongo |
| Единый error.code/message/details | Выполнено | `handler/response.go`, все API handlers | Handler tests, browser validation-details test |
| Graceful shutdown/readiness | Выполнено | `app/app.go`, `app/dependencies.go`, `worker/consumer.go`, `handler/health.go` | Lifecycle/health tests, app/Rabbit restart E2E |
| Docker Compose dependencies и volumes | Выполнено | `docker-compose/docker-compose.yml` | up --build, ps: 5 healthy; migrate exit 0; volumes сохранены |

## Дополнительное MongoDB

| Требование | Статус | Где реализовано | Как проверено |
|---|---|---|---|
| calculation_logs после каждого нового расчёта | Выполнено | WorkerService, CalculationLogRepository | Mongo integration, E2E count=1 |
| Уникальный _id=package_id | Выполнено | `repository/calculation_log.go` | Concurrent upsert и duplicate-log integration |
| Cost/rate Decimal128, timestamps и type metadata | Выполнено | `model/calculation.go`, Mongo log document | Mongo integration, реальные документы E2E |
| GET analytics/daily-sum | Выполнено | `handler/analytics.go`, `service/analytics.go` | Unit, browser E2E |
| Обязательная date YYYY-MM-DD, UTC [start,end) | Выполнено | AnalyticsService | Invalid-date, leap-day/year-end UTC tests; Mongo day boundaries |
| Session isolation | Выполнено | `$match session_id` | Mongo integration, foreign session E2E data=[] |
| Настоящий Aggregation Pipeline | Выполнено | `CalculationLogRepository.DailySum` | Реальные Mongo aggregation tests |
| Group по type ID, count, sort | Выполнено | `$group`, `$sum`, `$sort` | Несколько типов, переименование и сортировка в Mongo integration |
| Точная строковая сумма, пустой день data=[] | Выполнено | Decimal128 → decimal.StringFixed(2), model response | Сумма 0.10+0.20=0.30, большая сумма, empty tests |
| Индекс session_id+calculated_at | Выполнено | EnsureIndexes до старта consumer | Повторное создание индекса integration/startup |
| Retry лога не удваивает analytics | Выполнено | `$setOnInsert`, canonical WorkerService | Mongo integration и повтор сообщения после restart |

## Дополнительное RabbitMQ

| Требование | Статус | Где реализовано | Как проверено |
|---|---|---|---|
| POST публикует в packages.to_calculate после validation | Выполнено | PackageService + Publisher | Service/publisher tests, E2E |
| POST не записывает package прямо в PostgreSQL | Выполнено | Read-only service repository interface | Тест Create запрещает repo.Create |
| Durable queues, persistent, mandatory и publisher confirms | Выполнено | `messaging/topology.go`, `publisher.go` | Rabbit integration, restart RabbitMQ |
| Версионированное сообщение, decimals strings, trace | Выполнено | `messaging/package_message.go` | Validation/JSON tests, trace E2E |
| Consumer немедленно рассчитывает и сохраняет PostgreSQL | Выполнено | `worker/consumer.go`, WorkerService | Rabbit и PostgreSQL integration; browser E2E |
| Manual ACK после PostgreSQL+Mongo | Выполнено | Consumer.handle | ACK, Mongo failure и redelivery integration |
| Retry delay, ограниченные retries, DLQ | Выполнено | Retry queue TTL 5s; maxRetries 3; DLQ | Rabbit retry/DLQ tests |
| Failure forwarding оставляет source unacked | Выполнено | Confirmed PublishRaw перед ACK | Rabbit integration с отсутствующей retry/DLQ queue |
| Idempotency при redelivery | Выполнено | PostgreSQL canonical result + Mongo upsert | Concurrent PG/Mongo tests; duplicate E2E после restart |
| Scheduler отсутствует | Выполнено | Scheduler/config wiring удалены на Этапе 4 | Поиск scheduler/CALCULATION_INTERVAL |
| Fatal consumer error завершает app, без reconnect | Выполнено | App lifecycle | Unit и ожидаемый shutdown при restart RabbitMQ |
| Grace period, unacked unfinished redelivery | Выполнено | Consumer.Stop + app timeout | Drain/cancellation tests, штатный restart app |
| Eventual consistency документирована | Выполнено | Swagger POST, README, UI limited polling | Генерация/HTTP Swagger, browser E2E |

## Дополнительное Optimistic Locking

| Требование | Статус | Где реализовано | Как проверено |
|---|---|---|---|
| POST packages/{id}/assign-carrier | Выполнено | carrier handler/service/repository | Unit/browser E2E |
| carrier_id положительный int64, validator/v10 | Выполнено | AssignCarrierRequest | 0, negative, missing/null, fraction/overflow handler tests |
| UUID validation, session isolation | Выполнено | CarrierHandler, SQL predicates | Invalid UUID unit, foreign/missing PG integration и E2E |
| Атомарный UPDATE WHERE carrier_id IS NULL | Выполнено | `repository/carrier.go` | 12 concurrent goroutines: 1 win, 11 conflict, winner сохранён |
| Отдельный SELECT только после неуспешного UPDATE | Выполнено | `repository/carrier.go` | PG missing/foreign/already assigned integration |
| 200/404/409, включая повтор того же carrier | Выполнено | Domain sentinels + errors.Is | Handler/service/integration; E2E 200→409 |
| Нет data race | Выполнено | Все новые слои и tests | go test -race, integrations -race |

## Web UI

| Требование | Статус | Где реализовано | Как проверено |
|---|---|---|---|
| HTML/CSS/JS, GET /, same-origin cookie, Go static/embed | Выполнено | `web/*`, router | Router unit, headless browser |
| Форма name/weight/type/content | Выполнено | index.html/app.js | Реальная регистрация из UI |
| Types из API | Выполнено | loadTypes | Browser select заполнен ответом API |
| Список, pagination, filters | Выполнено | loadPackages, navigation/filter controls | Реальный список/filters; browser fixture page navigation |
| Name/weight/type/content/cost/status/carrier | Выполнено | Таблица packages | Browser E2E и screenshot |
| Null cost → «Не рассчитано» | Выполнено | app.js | Browser contract fixture |
| Сообщение registered и bounded polling | Выполнено | pollPackage, max 12 × 1.5s | Browser E2E, проверка реализации лимита |
| Assign carrier и понятный 409 | Выполнено | carrier form + API error.code | Реальное назначение; handler/E2E 409; фронтенд переводит code |
| Analytics date/table | Выполнено | analytics form | Реальные Mongo данные в browser |
| Стандартный error и validation details | Выполнено | api/notice | Browser error fixture; handler error contract tests |
| Mobile layout, отсутствие JS errors | Выполнено | styles.css, safe textContent rendering | Desktop/mobile browser, viewport 390px без внешнего overflow |

## Tests, документация и проверка запуска

| Требование | Статус | Где реализовано | Как проверено |
|---|---|---|---|
| Unit-tests и новые handler/service tests | Выполнено | service/carrier_test.go, analytics_test.go; handler/extensions_test.go; app/router_test.go | go test ./... |
| PostgreSQL integration + carrier concurrency | Выполнено | repository/carrier_integration_test.go и существующие PG tests | -tags=integration -race |
| Mongo analytics integration | Выполнено | calculation_log_integration_test.go | -tags=integration -race |
| RabbitMQ integration сохранены | Выполнено | messaging/worker integration tests | -tags=integration -race |
| gofmt / vet / race | Выполнено | Весь Go-проект | gofmt -w .; go vet ./...; go test -race ./... |
| golangci-lint | Выполнено | Установлен v2.13.2; без нового строгого config | golangci-lint run ./...: 0 issues |
| Swagger новые paths/statuses/модели/асинхронность | Выполнено | annotations, docs/docs.go, swagger.json/yaml | swag init; browser HTTP endpoints 200; новые paths в doc.json |
| README, architecture, run/test commands, env | Выполнено | README.md, .env.example | Сверка используемых config env; реальные команды Compose/tests |
| Go Doc основных exported API | Выполнено | Основные services/repositories/messaging/consumer/handlers/models | Проверка комментариев; vet/lint |
| Web UI/health/Swagger/RabbitMQ UI доступны | Выполнено | Router/Compose | Реальные HTTP проверки browser E2E |
| Нет неожиданных panic/fatal в app logs | Выполнено | slog lifecycle | Единственный ERROR — ожидаемая потеря consumer при намеренном restart RabbitMQ; app корректно остановился |
| Полный POST→worker→PG→Mongo→analytics→carrier→foreign→restart E2E | Выполнено | Реальный Compose и headless браузер | Посылка a795e93f-edd5-468e-a9c6-5094afa03534; стоимость 170.96; Mongo count=1; assign 200/409; foreign 404; после restart повтор сообщения ничего не изменил |

## Файлы Этапа 5

Созданы:

- `internal/model/analytics.go`;
- `internal/repository/carrier.go`, `carrier_integration_test.go`;
- `internal/service/carrier.go`, `carrier_test.go`, `analytics.go`, `analytics_test.go`;
- `internal/handler/carrier.go`, `analytics.go`, `extensions_test.go`;
- `internal/app/router_test.go`;
- `web/embed.go`, `index.html`, `app.js`, `styles.css`;
- `docs/FINAL_AUDIT.md`.

Изменены:

- `internal/model/package.go`, `calculation.go`;
- `internal/repository/calculation_log.go`, `calculation_log_integration_test.go`, `package.go`, `package_worker.go`, `package_type.go`;
- `internal/service/package.go`, `worker.go`, `exchange_rate.go`, `calculation.go`, `package_type.go` (последние изменения — Go Doc);
- `internal/handler/package.go`, `package_type.go`, `admin.go`, `health.go`;
- `internal/app/router.go`, `dependencies.go`;
- `internal/messaging/publisher.go`, `package_message.go`, `rabbitmq.go`, `topology.go`;
- `internal/worker/consumer.go` (Go Doc), `internal/exchange/client.go` (Close result явно обработан);
- `docs/docs.go`, `swagger.json`, `swagger.yaml`, `README.md`, `.env.example`.

На Этапе 5 файлы не удалялись, миграции не менялись, новых Go-зависимостей нет. Playwright установлен только во временную директорию для проверки UI, не в проект. `.env.local`/`.env.docker` и volumes сохранены.

## Остатки и ограничения

Невыполненных требований по указанному ТЗ и Этапу 5 не выявлено. Production-аутентификация и automatic reconnect не требовались; session cookie не является аутентификацией. При потере брокера необходим restart app. Legacy calculated без rate/time не восстанавливаются выдуманными данными. CBR был недоступен во время E2E — проверен предусмотренный fallback. Swagger generator выдаёт некритичное предупреждение о том, что в корне нет Go-файлов; генерация через cmd/api/main.go успешна.

Compose проверялся с сохранёнными существующими volumes. Пустая PostgreSQL schema проверялась integration-тестами миграций; существующие данные ради «чистого» запуска не удалялись. Суммы analytics и saved usd_to_rub_rate — точные строки; API деталей сохраняет существующий числовой формат стоимости. В UI pagination/null/error rendering дополнительно проверялись контрактными fixtures; основной бизнес-сценарий использовал реальные API/БД/брокер.

## Финальный hardening-pass

Архитектура, формула, queues/retry TTL, assign-carrier, analytics scope, Manual ACK и health/shutdown сохранены. Дополнены только registration metadata, Mongo verification и выдача сохранённого курса.

| Требование hardening | Статус | Реализация | Проверка |
|---|---|---|---|
| Optional Idempotency-Key, прежний API без header | Выполнено | PackageHandler, PackageService | Header/service tests, прежние tests |
| Key: один header, 1..128 printable ASCII | Выполнено | validator/v10 Var | Empty/long/multiple/unicode/max tests; общий error helper |
| Same session/key/canonical payload → same ID/time | Выполнено | SHA-256 + IdempotencyRepository.Reserve | Exact JSON normalization unit; реальный HTTP repeat |
| Другой payload → 409 | Выполнено | ErrIdempotencyKeyConflict, IDEMPOTENCY_KEY_CONFLICT | Все 4 business field variants в unit; HTTP E2E |
| Разные session независимы | Выполнено | PK(session_id,idempotency_key) | PostgreSQL integration, HTTP E2E |
| 10 concurrent → одна регистрация | Выполнено | Atomic INSERT ON CONFLICT DO NOTHING; SELECT winner | PostgreSQL -race; 10 concurrent HTTP requests |
| Новая migration, initial не изменены | Выполнено | 3_package_idempotency.up/down.sql | Пустые test schemas и down migration; real schema_migrations=3, dirty=false |
| Нет DB transaction во время RabbitMQ publish | Выполнено | Два завершённых pool statements внутри Reserve; Publish после возврата | Проверка кода: нет Begin/Tx; metadata не создаёт packages |
| Hash без raw JSON / float64 | Выполнено | RequestDecimal tokens → shopspring decimal → canonical struct → SHA-256 | JSON order/scale/exponent tests; near-boundary exact validation |
| Numeric JSON contract сохранён | Выполнено | RequestDecimal.UnmarshalJSON | Quoted numeric values по-прежнему INVALID_JSON |
| UI использует crypto UUID и сохраняет key после lost response | Выполнено | sessionStorage key+payload; reset после 201 | Chromium: route.fetch отправил первый POST до confirm, ответ abort; retry получил прежний ID/time; следующий submit новый key |
| Mongo existing log business consistency | Выполнено | Upsert + canonical read + verifyCalculationLog | Реальные Mongo conflict tests и no-overwrite checks |
| Exact Decimal128 и Mongo Date precision | Выполнено | decimal.Equal, UnixMilli | Отличие rate в последнем точном разряде → conflict; equivalent scale/time → success |
| Trace/type name semantics | Выполнено | Не сравниваются | Trace относится к попытке/recovery; name — mutable PG dictionary, сохранённый первый Mongo snapshot не перезаписывается |
| Conflict sentinel не игнорируется | Выполнено | ErrCalculationLogConflict, errors.Is | Worker propagation test |
| Mongo conflict permanent → DLQ с reason | Выполнено | Consumer.IsPermanent; error attrs package_id/trace_id | Consumer routing test: DLQ, ACK только после confirmed forwarding |
| Saved usd_to_rub_rate nullable exact string | Выполнено | PackageListItem JSON, существующий repo SELECT | null/exact 22-digit rate JSON test; реальные GET/PostgreSQL совпадают |
| UI rate, pending — | Выполнено | колонка «Курс USD/RUB» перед статусом | Chromium: saved 85.4811; pending fixture — |
| Swagger/README обновлены | Выполнено | Generated Swagger, README Idempotency-Key | HTTP spec: optional header, 409, rate string |
| Финальные unit/vet/race/lint | Выполнено | Весь проект | gofmt -w .; go test/vet/race; golangci-lint: 0 issues |
| PG/Mongo/Rabbit integrations | Выполнено | Существующие и новые tests | go test -race -tags=integration repository/messaging/worker |
| E2E duplicates не меняют результат/analytics | Выполнено | Реальный Compose | 12 публикаций одного ID: package count=1, Mongo count=1, cost=170.96, rate=85.4811, analytics package_count=1 |
| POST не INSERT packages; scheduler/config отсутствуют; один helper/validator | Выполнено | Code search internal/config/env | Только INSERT package_idempotency в registration repository; нет scheduler/CALCULATION_INTERVAL/http.Error/альтернативного encoder |

Алгоритм: validate request и тип → normalize exact decimals → hash → autocommit reservation → load stored ID/time → confirmed RabbitMQ publish → 201. Reservation намеренно сохраняется при publish failure или неизвестном исходе; retry переиспользует её. UUID/time имеют PostgreSQL microsecond precision. Worker получает повторные сообщения, сверяет PostgreSQL canonical result и Mongo business data; trace различных запросов законно отличается.

Mongo conflict сообщает конкретное поле, не заменяет canonical document и не запускает повторные вычисления. PostgreSQL остаётся источником результата. Mongo timestamp comparisons ограничены точностью BSON Date (milliseconds); decimal comparisons точные числовые, эквивалентный scale допустим. Package type ID сравнивается, имя — нет: в текущей модели ожидаемое имя повторно читается из изменяемого справочника, а исторический лог хранит первоначальное имя.

Добавлены hardening-файлы:

- `migrations/3_package_idempotency.up.sql`, `3_package_idempotency.down.sql`;
- `internal/model/idempotency.go`, `request_decimal.go`;
- `internal/repository/idempotency.go`, `idempotency_integration_test.go`;
- `internal/service/idempotency_test.go`;
- `internal/handler/idempotency_test.go`.

Изменены hardening-файлы:

- `internal/model/package.go`, `internal/validation/decimal.go`, `decimal_test.go`;
- `internal/service/package.go`, `package_test.go`, `worker_test.go`;
- `internal/handler/package.go`, `package_test.go`, `response.go`;
- `internal/app/dependencies.go`;
- `internal/repository/package_integration_test.go`, `calculation_log.go`, `calculation_log_integration_test.go`;
- `internal/worker/consumer.go`, `consumer_test.go`;
- `web/index.html`, `web/app.js`;
- `docs/docs.go`, `swagger.json`, `swagger.yaml`, `FINAL_AUDIT.md`, `README.md`.

Новых Go-зависимостей и изменений initial migrations нет. Блокирующих замечаний не выявлено. Ключи хранятся без автоматической очистки; это сохраняет гарантию idempotency, TTL не вводился. Без optional header гарантия повторной HTTP-регистрации не применяется. Установленный Yandex отказался запускаться headless в этом окружении; Chromium из временной папки успешно завершил UI E2E. Тестовые браузерные инструменты не добавлены в проект.
