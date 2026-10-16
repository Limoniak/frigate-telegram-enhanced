<script>
  // A translated text whose %s becomes a link (the text itself is never HTML).
  let { text, href, label } = $props();
  const parts = $derived(text.split("%s"));
</script>

{parts[0]}<a {href} target="_blank" rel="noopener">{label}</a>{parts[1] ?? ""}
