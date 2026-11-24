<template>
  <button @click="increment">{{ label }}: {{ count }}</button>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'

export interface Props {
  label: string
  step?: number
}

type Handler = (n: number) => void

enum Mode {
  Fast,
  Slow,
}

const props = defineProps<Props>()
const emit = defineEmits<{ (e: 'change', value: number): void }>()
const model = defineModel<number>()

const MAX = 10
let count = ref(0)
var legacy = 1
const doubled = computed(() => count.value * 2)

const format = (n: number) => `#${n}`
const parse = function (s: string) {
  return Number(s)
}

function increment() {
  const local = count.value + (props.step ?? 1)
  let inner = local
  count.value = inner
  emit('change', count.value)
}

async function load(): Promise<void> {
  const data = await fetch('/api')
  console.log(data)
}

class Tracker {
  total = 0
  add(n: number) {
    this.total += n
  }
}

defineExpose({ increment })
</script>

<style scoped>
button {
  color: red;
}
</style>
