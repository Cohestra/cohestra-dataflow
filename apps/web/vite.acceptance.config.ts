import { mergeConfig } from 'vite';
import config from './vite.config';

export default mergeConfig(config, {
  preview: {
    host: '127.0.0.1', port: 13002, strictPort: true,
    proxy: { '/api': 'http://127.0.0.1:14000' },
  },
});
