package db

import (
	"database/sql"
	"fmt"
)

// scanAll 泛型扫描辅助，消除 rows.Next() → Scan → append → nil→[] 的重复模式。
// T 是值类型（如 models.Session、models.Turn），bind 回调接受 *T 指针，
// 返回 Scan 所需的 []any 参数列表。scanAll 返回 []*T（指针切片）。
func scanAll[T any](rows *sql.Rows, bind func(*T) []any) ([]*T, error) {
	defer rows.Close()
	var items []*T
	for rows.Next() {
		var item T
		if err := rows.Scan(bind(&item)...); err != nil {
			return nil, fmt.Errorf("scan failed: %w", err)
		}
		items = append(items, &item)
	}
	if items == nil {
		items = []*T{}
	}
	return items, rows.Err()
}
