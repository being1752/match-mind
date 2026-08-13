import { createApp } from 'vue'
import TDesign from 'tdesign-mobile-vue'
import 'tdesign-mobile-vue/es/style/index.css'
import App from './App.vue'
import router from './router'
import './styles.css'

createApp(App).use(router).use(TDesign).mount('#app')
