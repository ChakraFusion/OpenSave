<script>
  // A paired or discovered device, by kind: a handheld or a computer.
  import Gamepad2 from 'lucide-svelte/icons/gamepad-2';
  import Monitor from 'lucide-svelte/icons/monitor';

  /** The device's reported type: 'deck' and 'handheld' (an Ally, a Legion
   *  Go — both offered in Settings) are handhelds, anything else a computer. */
  export let type = '';

  $: handheld = type === 'deck' || type === 'handheld';
</script>

<span class="device-icon" title={handheld ? 'Handheld' : 'Computer'}>
  <svelte:component this={handheld ? Gamepad2 : Monitor} size={19} />
</span>

<style>
  .device-icon {
    flex-shrink: 0;
    width: 38px;
    height: 38px;
    border-radius: 10px;
    display: grid;
    place-items: center;
    background: var(--bg-hover);
    color: var(--text-dim);
  }
</style>
