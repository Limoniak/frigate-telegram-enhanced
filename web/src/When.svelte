<script>
  // When to be notified: a few plain questions, the finer choices folded away.
  import { T, Tf } from "./lib/i18n.svelte.js";
  import { app, edit } from "./lib/store.svelte.js";
  import { nightOf, questionsFor, recipientHours, recipientValues, setNight, setRecipient } from "./lib/questions.js";
  import { matches } from "./lib/settings.js";
  import { clampScore, sensitivityEffect, unionOf } from "./lib/fields.js";
  import { labelText } from "./lib/labels.js";
  import Card from "./lib/Card.svelte";
  import Chips from "./lib/Chips.svelte";
  import Objects from "./lib/Objects.svelte";
  import Pills from "./lib/Pills.svelte";

  const night = $derived(nightOf(app.overlay));
  const questions = $derived(questionsFor(app.data, T, night));
  const chats = $derived(app.data.chats || []);
  const receiving = $derived(app.overlay.notify.chats || []);
  // A recipient picks among the objects everyone gets, or among all the tracked ones.
  const recipientObjects = $derived(app.overlay.notify.labels?.length ? app.overlay.notify.labels : unionOf(app.data, "labels"));
  const objectName = (l) => labelText(l, false, T);
  // What a recipient's folded line says: their objects and hours, or "Same as everyone".
  const summary = (hours, values) => {
    const parts = [];
    if (values.labels.length) parts.push(values.labels.map(objectName).join(", "));
    const h = hours.find((o) => matches(o.patch, values));
    if (h !== hours[0]) parts.push(h?.label ?? T("Custom"));
    return parts.join(" · ") || T("Same as everyone");
  };
  // A night must start and end at different times: until it does, the typed hours stay
  // in view, flagged, and nothing is saved. A night saved meanwhile (a reset) drops them.
  let nightDraft = $state(null);
  $effect.pre(() => { night; nightDraft = null; });
  const shownNight = $derived(nightDraft ?? night);
  const nightBad = $derived(!!nightDraft);
  function retime(n) {
    if (n.from && n.to && n.from !== n.to) {
      nightDraft = null;
      edit((o) => setNight(o, n));
    } else nightDraft = n;
  }
  const subs = $derived((app.data.sub_labels || []).slice().sort());
  const showKnown = $derived(subs.length || (app.overlay.notify.ignore_sub_labels || []).length || app.overlay.notify.ignore_known);
  const known = $derived(!!app.overlay.notify.ignore_known);
  const ms = $derived(app.overlay.notify.min_score || { default: 0 });
  const hasMore = $derived(questions.some((q) => q.more) || !!showKnown || app.data.mode !== "reviews");
  const perLabel = $derived(!!ms.by_label && Object.keys(ms.by_label).length > 0);

  // While dragging only the text changes; the value is kept on release.
  let drag = $state(null);
  const score = $derived(drag ?? ms.default ?? 0);
  const pct = (v) => Math.round(v * 100) + T("%");
</script>

{#snippet question(title, hint, body)}
  <div class="flex flex-col gap-2"><div>{title}</div>{#if hint}<div class="text-[12px] text-[var(--faint)]">{hint}</div>{/if}{@render body()}</div>
{/snippet}

<section id="when" class="mx-auto flex w-full max-w-3xl flex-col gap-4">
  <div class="flex flex-col gap-1"><span class="lbl">{T("When")}</span><h1 class="m-0 text-[26px] font-semibold tracking-tight">{T("When should you be notified?")}</h1></div>
  <Card>
    {#snippet objects()}<Objects label={T("What should be reported?")} />{/snippet}
    {@render question(T("What should be reported?"), T("Nothing selected = everything Frigate detects."), objects)}
    {#each questions.filter((q) => !q.more) as q (q.id)}
      {#snippet pills()}
        <Pills label={q.title} options={q.options} />
        {#if q.id === "night"}
          <div class="flex flex-wrap items-center gap-2 text-[12px] text-[var(--muted)]">
            {T("The night goes from")}
            <input type="time" class="field w-28" aria-label={T("Night starts at")} aria-invalid={nightBad ? "true" : undefined} aria-describedby={nightBad ? "night-error" : undefined}
              value={shownNight.from} onchange={(e) => retime({ ...shownNight, from: e.currentTarget.value })}>
            <!-- "to" stays with its hour when the line wraps. -->
            <span class="inline-flex items-center gap-2">{T("to")}
            <input type="time" class="field w-28" aria-label={T("Night ends at")} aria-invalid={nightBad ? "true" : undefined} aria-describedby={nightBad ? "night-error" : undefined}
              value={shownNight.to} onchange={(e) => retime({ ...shownNight, to: e.currentTarget.value })}></span>
          </div>
          {#if nightBad}<p id="night-error" class="m-0 text-[12px] text-[var(--err)]" role="alert">{T("Enter a start and an end that are different.")}</p>{/if}
        {/if}
      {/snippet}
      {@render question(q.title, q.hint, pills)}
    {/each}
  </Card>
  {#if chats.length > 1}
  <Card>
    {#snippet who()}<Chips label={T("Who receives the notifications?")} options={chats} value={receiving} onchange={(next) => edit((o) => { o.notify.chats = next; })} />{/snippet}
    {@render question(T("Who receives the notifications?"), T("Everyone gets the choices above. Open a name to give them less."), who)}
    {#each chats.filter((c) => receiving.includes(c)) as chat (chat)}
      {@const values = recipientValues(app.overlay, chat)}
      {@const hours = recipientHours(app.overlay.notify, values, T, night)}
      {@const pick = (patch) => edit((o) => setRecipient(o, chat, patch))}
      <details class="more" data-chat={chat}>
        <summary>{"👤 " + chat} <span class="text-[var(--muted)]">· {summary(hours, values)}</span></summary>
        <div class="mt-3 flex flex-col gap-2">
          <div class="text-[12px] text-[var(--faint)]">{T("Receives")} · {T("nothing selected = the same objects as everyone")}</div>
          <Chips label={chat + " – " + T("Receives")} options={recipientObjects} value={values.labels} name={objectName} onchange={(next) => pick({ labels: next })} />
          <div class="text-[12px] text-[var(--faint)]">{T("Hours")}</div>
          <Pills label={chat + " – " + T("Hours")} options={hours} {values} {pick} />
        </div>
      </details>
    {/each}
  </Card>
  {/if}
  {#if hasMore}
  <Card>
    <details class="more" open={false}>
      <summary>{T("More choices: bursts, sensitivity…")}</summary>
      <div class="mt-4 flex flex-col gap-5">
        {#each questions.filter((q) => q.more) as q (q.id)}
          {#snippet pills()}<Pills label={q.title} options={q.options} />{/snippet}
          {@render question(q.title, q.hint, pills)}
        {/each}
        {#if showKnown}
          {#snippet labels()}
            <div class="flex flex-col gap-2">
              <Pills label={T("Known labels")} options={[{ label: T("Report everything"), patch: { ignore_known: false } }, { label: T("🏷 Only unknown objects"), patch: { ignore_known: true } }]} />
              {#if !known}
                <div class="text-[12px] text-[var(--faint)]">{T("Don't notify for:")}</div>
                <Chips label={T("Don't notify for:")} options={subs} value={app.overlay.notify.ignore_sub_labels || []} free onchange={(next) => edit((o) => { o.notify.ignore_sub_labels = next; })} />
              {/if}
            </div>
          {/snippet}
          {@render question(T("Known labels"), T("Frigate takes a few seconds to recognize an object: with a label filter, notifications wait up to 5 s for it."), labels)}
        {/if}
        {#if app.data.mode !== "reviews"}
          {#snippet sens()}
            {#if perLabel}
              <p class="m-0 text-[12px] text-[var(--faint)]"><a href="#advanced">{T("Set per object in Advanced settings.")}</a></p>
            {:else}
              <div class="flex items-center gap-3">
                <input type="range" class="min-w-0 flex-1" min="0" max="0.95" step="0.05" aria-label={T("Minimum score")} value={score}
                  oninput={(e) => (drag = Number(e.currentTarget.value))}
                  onchange={(e) => { const v = clampScore(e.currentTarget.value); drag = null; edit((o) => { o.notify.min_score = { default: v }; }); }} />
                <span class="w-10 text-right">{pct(score)}</span>
              </div>
              <div class="flex justify-between gap-2 text-[12px] text-[var(--faint)]"><span>{T("◀ more notifications")}</span><span>{T("fewer false alarms ▶")}</span></div>
              <p class="m-0 text-[12px] text-[var(--muted)]">{sensitivityEffect(app.history || [], score, T, Tf)}</p>
            {/if}
          {/snippet}
          {@render question(T("Sensitivity"), T("Frigate's confidence that the object is real. Higher = fewer false alarms, but more risk of missing something."), sens)}
        {/if}
      </div>
    </details>
  </Card>
  {/if}
</section>
