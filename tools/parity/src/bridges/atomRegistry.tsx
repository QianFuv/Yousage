/** Original app registry and change subscription, without the React context provider. */
import { type Atom, AtomRegistry } from 'effect/reactivity';

export const appAtomRegistry = AtomRegistry.make();

/** Call `listener` with each value after the current one, as the original app registry does. */
export function subscribeAtomChanges<A>(atom: Atom.Atom<A>, listener: (value: A) => void): () => void {
  let current = true;
  return appAtomRegistry.subscribe(
    atom,
    (value) => {
      if (current) {
        current = false;
        return;
      }
      listener(value);
    },
    { immediate: true },
  );
}
