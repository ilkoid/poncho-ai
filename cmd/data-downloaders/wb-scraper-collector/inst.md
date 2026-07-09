Тест расширения → Go-сервер → PG
Среда: local dev RYZEN-ILKOID, PG 192.168.10.7:15432 / postgres / $PG_PWD. Сервер сам поднимает wbscraper-схему при старте. Только wb_data_test, никогда prod//var/db.

1. Smoke: mock-сервер (без БД, ~30 сек)
Terminal A:


cd cmd/data-downloaders/wb-scraper-collector
go run . --mock --addr 127.0.0.1:7780
Terminal B — POST тестового снимка:


curl -sX POST http://127.0.0.1:7780/snapshot -H 'Content-Type: application/json' -d '{
  "snapshot":"2026-07-09T12:00:00Z",
  "search_queries":[{"query_id":1,"query":"кроссовки","subject":"Обувь","brand":"Nike"}],
  "competitor_cards":[{"snapshot_ts":"2026-07-09T12:00:00Z","query_id":1,"nm_id":111,"brand":"Nike"}],
  "competitor_card_meta":[{"snapshot_ts":"2026-07-09T12:00:00Z","query_id":1,"nm_id":111,"vendor_code":"22123456","imt_name":"Кроссовки беговые","brand_name":"Nike","photo_count":6}]
}' | jq .
→ "counts":{...,"cards":1,...,"meta":1,...}. Повторить → те же counts (replace идемпотентен).

2. Реальный путь — локальная PG test-БД
Terminal A (сервер поднимает схему сам):


cd cmd/data-downloaders/wb-scraper-collector
go run . --config cmd/.configs/download-all/wb-scraper-collector.yaml \
  --backend postgres --pg-database wb_data_test --addr 127.0.0.1:7780
Terminal B — тот же curl (body из п.1), потом проверка строк:


PGPASSWORD="$PG_PWD" psql -h 192.168.10.7 -p 15432 -U postgres -d wb_data_test -c "
SELECT snapshot_ts,nm_id,brand_name,imt_name FROM competitor_card_meta ORDER BY snapshot_ts DESC LIMIT 5;
SELECT query_id,query,subject,brand FROM search_queries WHERE query='кроссовки';"
→ 1 строка в competitor_card_meta, 1 в search_queries.

Идемпотентность — повторить curl, затем:


PGPASSWORD="$PG_PWD" psql -h 192.168.10.7 -p 15432 -U postgres -d wb_data_test -tAc \
"SELECT count(*) FROM competitor_card_meta WHERE snapshot_ts='2026-07-09T12:00:00Z';"
→ 1 (не 2): ReplaceSnapshot = DELETE+INSERT, без дублей.

3. Через расширение (end-to-end, опционально)

cd extensions/poncho-wb-parser && npm run build
chrome://extensions → Dev mode → Load unpacked → dist/ → дашборд → «Настройки» → «Сброс снимков на сервер» → http://127.0.0.1:7780 → Сохранить → «Сбор» → запустить. В логе: ↗ снимок отправлен (N строк), в Terminal A: snapshot: ... cards=... meta=... compositions=....

4. ipFilter (опц.)
В yaml: server.allowed_ips: ["127.0.0.1"], рестарт → шапка покажет AllowedIPs. Чужой IP → 403.