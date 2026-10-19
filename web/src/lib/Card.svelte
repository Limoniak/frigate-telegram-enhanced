<script>
  // A Nocturne glass card: one pane of frosted glass on the night.
  let { id = undefined, class: cls = "", children } = $props();
</script>

<section {id} class="flex flex-col gap-4 rounded-[20px] bg-[var(--glass)] p-5 shadow-[inset_0_1px_1px_var(--glass-hi)] ring-1 ring-[var(--ring)] sm:p-6 {cls}">
  {@render children()}
</section>
