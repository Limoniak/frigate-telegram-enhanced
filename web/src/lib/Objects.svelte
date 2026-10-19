<script>
  // The objects to report, as chips of what Frigate tracks, with their emoji; none chosen
  // = everything. For a camera, a first "Default" chip follows the global choice.
  import { T } from "./i18n.svelte.js";
  import { app, change, edit } from "./store.svelte.js";
  import { clearKeys, effective, inheritsAll } from "./settings.js";
  import { labelText } from "./labels.js";
  import { unionOf } from "./fields.js";
  import Chips from "./Chips.svelte";

  let { camera = null, label } = $props();
  const options = $derived(unionOf(app.data, "labels", camera));
  const inherit = $derived(!!camera && inheritsAll(app.overlay, ["labels"], camera));
  const value = $derived(effective(app.overlay, camera).labels || []);
</script>

<div class="flex flex-wrap items-center gap-2">
  {#if camera}<button type="button" class="chip" aria-pressed={inherit} onclick={() => edit((o) => clearKeys(o, ["labels"], camera))}>{T("Default")}</button>{/if}
  <Chips {label} {options} {value} name={(l) => labelText(l, false, T)} onchange={(next) => change({ labels: next }, camera)} />
</div>
