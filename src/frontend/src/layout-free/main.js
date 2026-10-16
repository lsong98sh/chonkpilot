/**
 * 子工程「独立入口」参考实现 —— **不参与主工程构建**。
 *
 * UILAYOUT-1 §3.1：并入主工程后**不再保留独立 dev 服务器**，由宿主的
 * `src/frontend` 统一构建；宿主装配（区块注册表 / 命令落点 / 持久化 /
 * `MainLayout.vue` 换装）见 §7 P3。
 *
 * 这里保留的是机制层的**最小可运行装配**（`createLayoutEngine` + `LfApp`）：
 * 未注入任何宿主依赖时，views/components 为空 → 容器渲染「该 view 暂未实现」占位。
 */
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import './styles/global.css'
import { createLayoutEngine } from './engine.js'

const app = createApp(App)
app.use(createPinia())

// 机制层门面：宿主在此注入 mq / views / persist / i18n / icons / components / dialog
createLayoutEngine().install(app)

app.mount('#app')
