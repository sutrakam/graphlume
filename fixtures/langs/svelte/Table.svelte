<script lang="ts">
  let { rows }: { rows: number[] } = $props();

  let selection = $state({});

  const table = createTable({
    get data() {
      return rows;
    },
    state: {
      get rowSelection() {
        return selection;
      },
    },
    onRowSelectionChange: () => 1,
    getRowId(row) {
      return String(row);
    },
  });

  function reset() {
    selection = {};
  }

  class Cell {
    get value() {
      return 1;
    }
    render() {}
  }
</script>

<button onclick={reset}>reset</button>
