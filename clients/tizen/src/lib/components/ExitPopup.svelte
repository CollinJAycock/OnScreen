<script lang="ts">
  // Return on the app's first screen (lib/appExit): Exit / Cancel, the
  // exit confirmation Samsung's Return Key Policy asks the app to build as
  // an HTML element. The options dialog's own handling: the
  // D-pad stays in it, the pointer hovers and clicks its buttons, and Back
  // cancels (its back-stack entry). Modal (focusModal): nothing behind it
  // takes the ring while it's up. Starts on Cancel, so a second stray Return
  // or OK doesn't end the app; Return, then ↑ and OK, does.
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
