import { defineConfig } from 'vite';
import eslintPlugin from 'vite-plugin-eslint';
import { resolve } from 'path';
import mkcert from 'vite-plugin-mkcert';
import path from 'node:path';
import { viteStaticCopy } from 'vite-plugin-static-copy';

export default defineConfig({
    server: {
        port: 3009,
        host: 'local-fleet-lite.dimo.org',
        https: true,
    },
    resolve: {
        tsconfigPaths: true,
    },
    build: {
        chunkSizeWarningLimit: 1000,
        rollupOptions: {
            input: {
                main: resolve(__dirname, 'index.html'),
                login: resolve(__dirname, 'login.html'),
            },
        },
    },
    plugins: [
        mkcert({
            keyPath: 'key.pem',
            certFileName: 'cert.pem',
            savePath: path.resolve(process.cwd(), '.mkcert'),
            hosts: ['localhost', '127.0.0.1', 'local-fleet-lite.dimo.org'],
        }),
        eslintPlugin(),
        viteStaticCopy({
            targets: [
                {
                    src: 'src/assets/*',
                    dest: 'assets',
                },
            ],
        }),
    ],
});
