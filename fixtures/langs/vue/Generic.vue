<!-- <script>const commented = 1</script> -->
<script lang="ts">
export const SHARED = 'shared'

export function sharedHelper(): string {
  return SHARED
}
</script>

<script setup lang="ts" generic="T extends Record<string, unknown>">
const { rows, selected = [] } = defineProps<{ rows: T[]; selected?: T[] }>()

function pick(row: T): T {
  return row
}
</script>

<template>
  <div v-for="row in rows" :key="row.id">{{ pick(row) }}</div>
</template>
