<script>
  // A Nocturne glass card: an outer shell and an inner core, like a pane in its frame.
  let { id = undefined, class: cls = "", children } = $props();
</script>

<section {id} class="rounded-[22px] p-[5px] bg-[var(--shell)] ring-1 ring-[var(--ring)] {cls}">
  <div class="flex flex-col gap-4 rounded-[17px] bg-[var(--glass)] p-5 shadow-[inset_0_1px_1px_var(--glass-hi)] sm:p-6">
    {@render children()}
  </div>
</section>
