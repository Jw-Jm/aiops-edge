const { chromium } = require('/Users/mssc/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright');
const fs = require('fs');
const path = require('path');

const root = '/Users/mssc/Documents/Code/agent/aiops';
const html = path.join(root, 'docs/design/aiops-final-ui-prototype.html');
const output = path.join(root, 'docs/design/ui-renders');
const views = [
  ['01-ai-operations', 'ai'],
  ['02-platform-overview', 'overview'],
  ['03-cluster-overview', 'cluster'],
  ['04-full-chain-observability', 'chain'],
  ['05-knowledge-graph', 'graph'],
  ['06-knowledge-base', 'knowledge'],
  ['07-reports', 'reports'],
  ['08-settings', 'settings'],
];

(async () => {
  fs.mkdirSync(output, { recursive: true });
  const browser = await chromium.launch({
    headless: true,
    executablePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    args: ['--allow-file-access-from-files'],
  });
  const page = await browser.newPage({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 1.5 });
  for (const [name, view] of views) {
    const query = `view=${encodeURIComponent(view)}`;
    await page.goto(`file://${html}?${query}`, { waitUntil: 'load' });
    await page.screenshot({ path: path.join(output, `${name}.png`), fullPage: false });
  }
  await browser.close();
})();
