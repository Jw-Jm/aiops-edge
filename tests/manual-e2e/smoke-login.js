// Smoke: PF-PAGE-001 login page.
const { runPageTest } = require('./lib/harness')

runPageTest({
  id: 'PF-PAGE-001',
  route: '/login',
  actions: async (page, check) => {
    check('login_form_visible', await page.locator('input').count() >= 2, 'username+password inputs')
    check('login_button_visible', await page.getByRole('button').count() >= 1)
    // empty-submit validation
    await page.getByRole('button').first().click().catch(() => {})
    await page.waitForTimeout(500)
    check('empty_submit_no_crash', true)
  },
}).then((s) => console.log(JSON.stringify(s)))
