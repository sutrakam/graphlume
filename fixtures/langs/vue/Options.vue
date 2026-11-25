<template>
  <ul>
    <li v-for="item in items" :key="item.id">{{ pluralize(item.n) }}</li>
  </ul>
</template>

<script>
import Child from './Child.vue'

const PAGE_SIZE = 20

export const helper = (x) => x * 2

export default {
  name: 'Options',
  components: { Child },
  props: {
    items: { type: Array, default: () => [] },
  },
  data() {
    return { page: 1 }
  },
  computed: {
    total() {
      return this.items.length
    },
    pages: function () {
      return Math.ceil(this.total / PAGE_SIZE)
    },
  },
  watch: {
    page: function (value) {
      this.load(value)
    },
    items: () => {},
  },
  created() {
    this.load(1)
  },
  methods: {
    pluralize: n => n + (n === 1 ? ' item' : ' items'),
    async load(page) {
      const result = await fetch('/items?page=' + page)
      return result
    },
    reset: function () {
      this.page = 1
    },
  },
}
</script>
