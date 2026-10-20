<script>
  // The service's mark, Ouverture: an open ring (a lens), its pupil, and a dot leaving
  // through the gap. The heavy cut of the favicon, made for 16-32 px; its colors follow
  // the page's theme (--logo-ring, --logo-dot).
  let { class: cls = "" } = $props();
</script>

<svg class={cls} viewBox="0 0 400 400" aria-hidden="true">
  <g transform="translate(191.3 208.7)">
    <path d="M110.3 -19.4A112 112 0 1 1 19.4 -110.3" fill="none" stroke="var(--logo-ring)" stroke-width="62" stroke-linecap="round" />
    <circle r="54" fill="var(--logo-dot)" />
    <circle cx="124.5" cy="-124.5" r="36" fill="var(--logo-dot)" />
  </g>
</svg>
