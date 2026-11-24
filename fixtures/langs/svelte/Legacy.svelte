<script context="module" lang="ts">
  export const ENTER_KEY = 13

  export function preload(id: string): Promise<void> {
    return Promise.resolve()
  }
</script>

<script lang="ts">
  import { onMount } from 'svelte'

  export let title: string
  export let items = []
  export let visible = true

  interface Todo {
    id: number
    done: boolean
  }

  type Filter = 'all' | 'done'

  const MAX_ITEMS = 100
  let currentFilter: Filter = 'all'
  let editing = null
  var legacyFlag = false

  $: filtered = items.filter((t) => currentFilter === 'all' || t.done)
  $: remaining = filtered.length
  $: if (remaining > MAX_ITEMS) {
    console.warn('too many')
  }
  $: {
    visible = remaining > 0
  }

  const toggle = (todo: Todo) => {
    todo.done = !todo.done
  }

  function remove(index: number) {
    const removed = items[index]
    let next = items.slice()
    next.splice(index, 1)
    items = next
    return removed
  }

  function handleKey(event: KeyboardEvent) {
    if (event.which === ENTER_KEY) editing = null
  }

  onMount(() => {
    const timer = setInterval(() => {}, 1000)
    return () => clearInterval(timer)
  })
</script>

<h1>{title}</h1>
{#each filtered as todo}
  <li on:click={() => toggle(todo)}>{todo.id}</li>
{/each}

<style>
  h1 {
    color: purple;
  }
</style>
