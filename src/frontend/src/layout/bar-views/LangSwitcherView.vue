<!--
  栏视图条目 `lang-switcher`（UILAYOUT-1 §7 P4① / 裁决 2）：状态栏语言切换（实时勾选态）。
  复用既有 `views/lang/LangSwitcher.vue`（其按钮已走 `lang-select` 既有主题）。
-->
<template>
  <LangSwitcher />
</template>

<script setup>
import LangSwitcher from '../../views/lang/LangSwitcher.vue'
</script>
