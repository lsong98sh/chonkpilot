<template>
  <div class="b-table-wrapper" :class="`b-table--${size}`">
    <table class="b-table">
      <thead>
        <tr>
          <th
            v-for="col in columns"
            :key="col.prop || col.type"
            :style="getColStyle(col)"
            class="b-table-th"
          >
            {{ col.type === 'index' ? '#' : col.label }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!data || data.length === 0">
          <td :colspan="columns.length" class="b-table-empty">
            {{ emptyText }}
          </td>
        </tr>
        <tr v-for="(row, rowIndex) in data" :key="rowIndex">
          <td
            v-for="col in columns"
            :key="col.prop || col.type + rowIndex"
            :style="getColStyle(col)"
            class="b-table-td"
          >
            <template v-if="col.type === 'index'">
              {{ rowIndex + 1 }}
            </template>
            <template v-else-if="col.type === 'action'">
              <slot name="action" :row="row" :index="rowIndex" />
            </template>
            <template v-else>
              {{ row[col.prop] }}
            </template>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup>
defineProps({
  columns: { type: Array, required: true },
  data: { type: Array, default: () => [] },
  emptyText: { type: String, default: '暂无数据' },
  size: { type: String, default: 'small' },
})

function getColStyle(col) {
  const style = {}
  if (col.width) style.width = typeof col.width === 'number' ? col.width + 'px' : col.width
  if (col.minWidth) style.minWidth = typeof col.minWidth === 'number' ? col.minWidth + 'px' : col.minWidth
  if (col.align) style.textAlign = col.align
  return style
}
</script>

<style scoped>
.b-table-wrapper {
  width: 100%;
  overflow-x: auto;
}

.b-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-size-sm, 13px);
}

.b-table-th {
  padding: 8px 12px;
  font-weight: 600;
  text-align: left;
  white-space: nowrap;
  border-bottom: 1px solid var(--border, #dee2e6);
  background: var(--bg-secondary, #fff);
  color: var(--text-secondary, #495057);
  user-select: none;
}

.b-table-td {
  padding: 7px 12px;
  border-bottom: 1px solid var(--border, #dee2e6);
  color: var(--text-primary, #212529);
  line-height: 1.4;
}

.b-table--medium .b-table-th {
  padding: 10px 14px;
}
.b-table--medium .b-table-td {
  padding: 9px 14px;
}

.b-table tbody tr:hover {
  background: var(--bg-hover, #e9ecef);
}

.b-table-empty {
  text-align: center;
  padding: 24px 12px;
  color: var(--text-muted, #6c757d);
  font-size: var(--font-size-sm, 13px);
}
</style>
