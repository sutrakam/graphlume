<script setup>
import { computed } from 'vue'

const rows = computed(() => ({
  async onClick() {
    await save()
  },
  label: 'row',
}))

const table = createTable({
  get data() {
    return rows.value
  },
  state: {
    get selection() {
      return {}
    },
  },
  onChange: () => 1,
  render() {
    return null
  },
})

const lookup = {
  find() {},
}

function save() {}
</script>

<script>
export default {
  name: 'Arguments',
  get title() {
    return 'x'
  },
  methods: {
    run() {},
  },
}
</script>
