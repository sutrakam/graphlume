<template>
  <div class="classic">{{ message }}</div>
</template>

<script>
module.exports = {
  data: function () {
    return { message: 'hi' }
  },
  methods: {
    greet: function () {
      return this.message
    },
    shout() {
      return this.message.toUpperCase()
    },
  },
}
</script>
