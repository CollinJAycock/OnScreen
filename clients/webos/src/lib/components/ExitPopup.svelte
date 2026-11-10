<script lang="ts">
  // Back on the app's first screen (lib/appExit): Exit / Cancel, LG's
  // popup for webOS TV 6.0 to 22. The options dialog's own handling: the
  // D-pad stays in it, the pointer hovers and clicks its buttons, and Back
  // cancels (its back-stack entry). Modal (focusModal): nothing behind it
  // takes the ring while it's up. Starts on Cancel, so a second stray Back
  // or OK doesn't end the app; Back, then ↑ and OK, does.
  import OptionsDialog from './OptionsDialog.svelte';
  import { focusModal } from '$lib/focus/focusable';
  import { i18n } from '$lib/i18n/index.svelte';

  interface Props {
    onexit: () => void;
    oncancel: () => void;
  }
  let { onexit, oncancel }: Props = $props();
</script>

<div class="exit-popup" use:focusModal>
  <OptionsDialog
    title={$i18n('exit.title')}
    options={[{ label: $i18n('exit.exit'), onselect: () => onexit() }]}
    oncancel={() => oncancel()}
    focusCancel
  />
</div>
