#!/usr/bin/env bash
set -euo pipefail

DB="/home/ilkoid/go-workspace/src/poncho-ai/cmd/data-analyzers/analyze-wb-feedbacks/quality_reports.db"
CSV="/home/ilkoid/go-workspace/src/poncho-ai/cmd/data-analyzers/analyze-wb-feedbacks/quality_reports.csv"
TMP=$(mktemp)

sqlite3 "$DB" -csv -utf8 ".headers on" "
SELECT
    product_nm_id,
    supplier_article,
    REPLACE(product_name, char(10), ' ') AS product_name,
    REPLACE(CAST(avg_rating AS TEXT), '.', ',') AS avg_rating,
    feedback_count,
    REPLACE(quality_summary, char(10), ' ') AS quality_summary,
    analyzed_from,
    analyzed_to,
    model_used
FROM product_quality_summary
ORDER BY product_nm_id;
" > "$TMP"

printf '\xEF\xBB\xBF' > "$CSV"
cat "$TMP" >> "$CSV"
rm "$TMP"

echo "Exported $(tail -n +2 "$CSV" | wc -l) rows to $CSV"
