<script setup>
import { useToast } from '../composables/useToast'
const { message } = useToast()
</script>
<template><transition name="fade"><div v-if="message" class="toast">{{ message }}</div></transition></template>
