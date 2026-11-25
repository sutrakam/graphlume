<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    label: string
    children?: Snippet
  }

  let { label, count = 0, class: className, keys: parents = [], open = $bindable(false), children }: Props = $props()

  let position = $state(-1)
  const doubled = $derived(count * 2)
  let total = $derived.by(() => count + position)

  function bump() {
    const next = count + 1
    count = next
  }

  const reset = () => {
    position = -1
  }

  $effect(() => {
    console.log(label, position)
  })
</script>

{#snippet row(item: string, index: number)}
  <li>{index}: {item}</li>
{/snippet}

{#snippet header()}
  {#snippet nested()}
    <b>{label}</b>
  {/snippet}
  {@render nested()}
{/snippet}

<!-- {#snippet commented(x)} {/snippet} -->

<button class={className} onclick={bump}>{doubled} {total}</button>
{@render row('a', 0)}
{@render children?.()}
