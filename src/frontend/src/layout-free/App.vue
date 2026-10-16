<template>
  <!-- 全局唯一的右键菜单：布局树 / 预览 / 标签页都共用它。
       ⚠️ 不要再在别的组件里挂第二份 LfContextMenu —— 它读的是全局单例
       `edit.contextMenu`，挂两份会在同一坐标渲染两个 `.ctx-mask`，靠后的那份吃掉点击。 -->
  <LfContextMenu />
  <LfHomePage />
</template>

<script setup>
import LfContextMenu from './components/LfContextMenu.vue'
import LfHomePage from './views/LfHomePage.vue'
</script>
