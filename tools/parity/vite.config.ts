/** Isolated reference graph; bridges only app plumbing, never pure algorithms. */
import { defineConfig } from 'vitest/config';
import tailwind from '@tailwindcss/vite';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const ROOT = fileURLToPath(new URL('.', import.meta.url));

export default defineConfig({
  resolve: { alias: { lucide: path.join(ROOT, 'node_modules/lucide/dist/esm/lucide/src/lucide.js') } },
  plugins: [
    {
      name: 'reference-plumbing',
      enforce: 'pre',
      /** Resolve the original app's utility and unused ACP plumbing to fixture bridges. */
      resolveId(source, importer) {
        if (source === '~/lib/utils' || (source.endsWith('/lib/utils') && importer?.includes('/upstream/'))) return path.join(ROOT, 'src/bridges/utils.tsx');
        if (source === '~/components/MorphIcon') return path.join(ROOT, 'upstream/apps/web/src/components/MorphIcon.tsx');
        if (source === '~/lib/color' || source === '~/lib/observeResize') return path.join(ROOT, 'upstream/apps/web/src', source.slice(2) + '.ts');
        if (source === '~/lib/windowControlsOverlay') return path.join(ROOT, 'src/bridges/titlebar.tsx');
        if (source === '@t3tools/contracts') return path.join(ROOT, 'src/bridges/contracts.tsx');
        if (source === '@t3tools/client-runtime/state/provider-instance-display') return path.join(ROOT, 'src/bridges/instance.tsx');
        if (source.endsWith('/settings/AcpRegistryIcon')) return path.join(ROOT, 'src/bridges/acp.tsx');
        if (source.endsWith('/settings/providerDriverMeta') || source.endsWith('/ProviderPackageIcon')) return path.join(ROOT, 'src/bridges/providers.tsx');
        if (source.startsWith('@t3tools/shared/')) return path.join(ROOT, 'upstream/packages/shared/src', source.slice('@t3tools/shared/'.length) + '.ts');
        if (source.startsWith('@t3tools/provider-core/')) return path.join(ROOT, 'upstream/packages/provider-core/src', source.slice('@t3tools/provider-core/'.length) + '.ts');
        if (source.startsWith('~/components/ui/')) {
          const name = source.slice('~/components/ui/'.length);
          return path.join(ROOT, 'upstream/apps/web/src/components/ui', name + (name === 'dialog-styles' ? '.ts' : '.tsx'));
        }
      },
    },
    tailwind(),
  ],
  test: { include: ['src/**/*.test.tsx'] },
});
