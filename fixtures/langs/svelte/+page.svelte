<script>
  export let data

  const greeting = 'hello'

  function shout() {
    return greeting.toUpperCase()
  }
</script>

<h1>{data.title} {greeting}</h1>
