import { dualTest } from '../../lib/dual';

dualTest(
  'login with wrong password, then login and logout',
  async (s) => {
    const { page } = s;
    await page.goto('/projects/ecookbook/issues/new');
    s.noteURL('redirected to login');
    await s.noteText('flash before login', '#flash_notice, #flash_error');
    s.takeRequests();

    await page.fill('#username', 'jsmith');
    await page.fill('#password', 'wrong');
    await page.click('#login-submit');
    await page.waitForLoadState('load');
    await s.noteText('error flash', '#flash_error');
    s.noteURL('after failed login');
    s.note('username kept', await s.value('#username'));
    s.noteRequests('failed login');

    await page.fill('#password', 'jsmith');
    await page.click('#login-submit');
    await page.waitForLoadState('load');
    s.noteURL('back_url honored');
    await s.noteText('logged in as', '#loggedas');
    s.noteRequests('login');

    await page.click('#account a.logout');
    await page.waitForLoadState('load');
    s.noteURL('after logout');
    await s.noteText('account menu', '#account');
    s.noteRequests('logout');
  },
);
