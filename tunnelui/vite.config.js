import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  base: '/__simple_ng_internal__/tunnel/',
  build: { outDir: '../internal/client/tunnelui/dist', emptyOutDir: true },
})
