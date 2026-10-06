<script lang="ts">
  // Back on the app's first screen (lib/appExit): Exit / Cancel, LG's
  // popup for webOS TV 6.0 to 22. The options dialog's own handling: the
  // D-pad stays in it, the pointer hovers and clicks its buttons, and Back
  // cancels (its back-stack entry). Modal (focusModal): nothing behind it
  // takes the ring while it's up. Starts on Cancel, so a second stray Back
  // or OK doesn't end the app; Back, then ↑ and OK, does.
  import OptionsDialog from './OptionsDialog.svelte';
  import { focusModal } from '$lib/focus/focusable';

  interface Props {
    onexit: () => void;
    oncancel: () => void;
  }
  let { onexit, oncancel }: Props = $props();
</script>

<div class="exit-popup" use:focusModal>
  <OptionsDialog
    title="Exit OnScreen?"
    options={[{ label: 'Exit', onselect: () => onexit() }]}
    oncancel={() => oncancel()}
    focusCancel
  />
</div>
