import { fileURLToPath } from 'node:url'

// Nonliteral import keeps Vite's config bundler from rebundling Vite itself.
const viteURL = new URL('../admin/node_modules/vite/dist/node/index.js', import.meta.url)
const { defineConfig } = await import(viteURL.href)
const local = value => fileURLToPath(new URL(value, import.meta.url))

export default defineConfig({
  root: local('./acceptance/'),
  esbuild: { jsx: 'automatic' },
  define: {
    __DEV__: 'true',
    'process.env.NODE_ENV': JSON.stringify('development'),
    'process.env.EXPO_PUBLIC_API_URL': JSON.stringify(''),
    'process.env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY': JSON.stringify('pk_test_fixture'),
  },
  resolve: {
    alias: [
      { find: /^react-native$/, replacement: local('./acceptance/native.tsx') },
      { find: /^expo-location$/, replacement: local('./acceptance/location.ts') },
      { find: /^@stripe\/stripe-react-native$/, replacement: local('./acceptance/stripe.ts') },
      { find: /^react-dom(?=\/|$)/, replacement: local('./node_modules/react-dom') },
      { find: /^react(?=\/|$)/, replacement: local('./node_modules/react') },
    ],
  },
  plugins: [{
    name: 'acceptance-only-screen-icon',
    enforce: 'pre',
    transformIndexHtml: {
      order: 'post',
      // Vite injects its client even with hmr:false. Acceptance needs no socket.
      handler: html => html.replace(/<script\b[^>]*src="\/@vite\/client"[^>]*><\/script>/g, ''),
    },
    transform(code, id) {
      if (id.split('?')[0] !== local('./app/index.tsx')) return
      const icon = /require\(["']\.\.\/assets\/icon\.png["']\)/g
      if ([...code.matchAll(icon)].length !== 1) throw new Error('Expected exactly one screen icon require')
      return code.replace(icon, JSON.stringify('data:image/svg+xml,%3Csvg xmlns="http://www.w3.org/2000/svg"/%3E'))
    },
  }],
  server: { hmr: false, fs: { allow: [local('./')] } },
})