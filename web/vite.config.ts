import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

// The game definitions live in ../server/src and are imported by the client too;
// dedupe makes them use this package's copy of boardgame.io.
export default defineConfig({ plugins: [vue()], resolve: { dedupe: ['boardgame.io'] } });
