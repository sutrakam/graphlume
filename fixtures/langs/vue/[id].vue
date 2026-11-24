<script setup>
import { useRoute } from 'vue-router'

const route = useRoute()
const id = route.params.id

function back() {
  history.back()
}
</script>

<template>
  <h1 @click="back">{{ id }}</h1>
</template>
