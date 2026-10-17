<script>
  // An on/off switch: a button with role="switch", so it is announced and usable
  // with the keyboard.
  let { checked, label, onchange, id = undefined } = $props();
</script>

<button type="button" role="switch" {id} aria-checked={checked} aria-label={label} onclick={() => onchange(!checked)}
  class="relative h-[21px] w-9 flex-none rounded-full transition-colors duration-500 ease-[var(--ease-spring)] {checked ? 'bg-[var(--accent)]' : 'bg-[var(--ring)]'}">
  <span class="absolute top-[2.5px] left-[2.5px] h-4 w-4 rounded-full bg-white shadow transition-transform duration-500 ease-[var(--ease-spring)] {checked ? 'translate-x-[15px]' : ''}"></span>
</button>
