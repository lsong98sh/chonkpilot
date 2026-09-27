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
            <!-- 表头具名插槽（按列 prop/type）：未提供时回落纯文本 label（如 `#header-mode` 可挂 ? tooltip） -->
            <slot :name="'header-' + (col.prop || col.type)" :col="col">{{ col.type === 'index' ? '#' : col.label }}</slot>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!data || data.length === 0">
          <td :colspan="columns.length" class="b-table-empty">
            {{ emptyText }}
          </td>
        </tr>
        <tr v-for="(row, rowIndex) in data" :key="rowIndex" :class="rowClassOf(row, rowIndex)">
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
            <template v-else-if="col.prop">
              <!-- 具名插槽（按列 prop 名）：未提供时回落原样渲染该字段 -->
              <slot :name="col.prop" :row="row" :index="rowIndex">{{ row[col.prop] }}</slot>
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
const props = defineProps({
  columns: { type: Array, required: true },
  data: { type: Array, default: () => [] },
  emptyText: { type: String, default: '' },
  size: { type: String, default: 'small' },
  // 行级 class（可选）：函数 (row, index) => class 或静态 class/对象/数组。
  // 用于按行状态标红/高亮（如记忆类别超阈值 → is-over），不影响无传入时的既有渲染。
  rowClass: { type: [Function, String, Object, Array], default: null },
})

// rowClassOf 解析行 class（函数式按行求值；其余原样返回）。
function rowClassOf(row, index) {
  return typeof props.rowClass === 'function' ? props.rowClass(row, index) : props.rowClass
}

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
  font-size: var(--font-size-sm);
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

/* 操作列按钮：保证足够大的点击区域 + 按钮间留白，避免"只能点到文字" */
.b-table-td .b-btn {
  min-width: 52px;
  margin: 2px;
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
  font-size: var(--font-size-sm);
}
</style>
