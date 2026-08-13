import { ref } from 'vue'

const message = ref('')
let timer
export function useToast() {
  function show(value) {
    message.value = value
    clearTimeout(timer)
    timer = setTimeout(() => { message.value = '' }, 2600)
  }
  return { message, show }
}
