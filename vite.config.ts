import { defineConfig } from 'vite';
export default defineConfig({ server: { port: 5178, proxy: { '/api': 'http://127.0.0.1:8765' } }, build: { target: 'es2022' } });
