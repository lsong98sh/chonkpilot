<template>
  <div class="lang-switcher">
    <Button
      v-for="lang in languages"
      :key="lang.code"
      class="lang-btn"
      :class="{ active: currentLocale === lang.code }"
      text
      size="mini"
      v-mq:[EventNames.langSelect].click="{ lang: lang.code }"
      :title="lang.label"
    >
      {{ lang.flag }}
    </Button>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '../../components/ui'
import { EventNames } from '../../events/event-names'
import { SUPPORTED_LANGUAGES } from '../../plugins/i18n'

const { locale } = useI18n()

const currentLocale = computed(() => locale.value)

const languages = SUPPORTED_LANGUAGES
</script>

<style scoped>
.lang-switcher {
  display: flex;
  align-items: center;
  gap: 2px;
}
.lang-btn {
  background: none;
  border: 1px solid transparent;
  border-radius: 4px;
  cursor: pointer;
  padding: 2px 4px;
  font-size: 14px;
  line-height: 1;
  transition: all 0.15s;
  opacity: 0.5;
}
.lang-btn:hover {
  opacity: 0.8;
  background: var(--bg-hover, rgba(0,0,0,0.05));
}
.lang-btn.active {
  opacity: 1;
  border-color: var(--border, #dee2e6);
  background: var(--bg-hover);
}
</style>
