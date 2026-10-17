<script>
  // What each recipient receives: some objects, some hours. Without a restriction a
  // recipient receives everything the cameras send.
  import { T } from "./lib/i18n.svelte.js";
  import { app, edit } from "./lib/store.svelte.js";
  import { RECIPIENT_OBJECTS, RECIPIENT_WHEN, recipientValues, setRecipient } from "./lib/questions.js";
  import Card from "./lib/Card.svelte";
  import Pills from "./lib/Pills.svelte";
</script>

<section id="recipients" class="flex max-w-3xl flex-col gap-4">
  <div class="flex flex-col gap-1"><span class="lbl">{T("Recipients")}</span><p class="m-0 text-[var(--muted)]">{T("By default, everyone receives everything the cameras send. Here, limit someone to some objects or some hours.")}</p></div>
  {#each app.data.chats || [] as chat (chat)}
    <Card>
      <div data-chat={chat} class="flex flex-col gap-3">
        <div class="font-medium">{"👤 " + chat}</div>
        <div class="text-[12px] text-[var(--faint)]">{T("Receives")}</div>
        <Pills label={T("Receives")} options={RECIPIENT_OBJECTS(T)} values={recipientValues(app.overlay, chat)} pick={(patch) => edit((o) => setRecipient(o, chat, patch))} />
        <div class="text-[12px] text-[var(--faint)]">{T("When")}</div>
        <Pills label={T("When")} options={RECIPIENT_WHEN(T)} values={recipientValues(app.overlay, chat)} pick={(patch) => edit((o) => setRecipient(o, chat, patch))} />
      </div>
    </Card>
  {/each}
</section>
