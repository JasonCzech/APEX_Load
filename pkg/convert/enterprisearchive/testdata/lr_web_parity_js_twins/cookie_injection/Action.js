function Action()
{
	web.addCookie('consent=yes');
	web.addCookie('sid=injected');

	web.url(
		{
			name : 'home',
			url : 'http://parity.test/app/home',
			resource : 0
		}
	);

	web.url(
		{
			name : 'account',
			url : 'http://parity.test/app/account',
			resource : 0
		}
	);

	web.removeCookie('consent');

	web.url(
		{
			name : 'final',
			url : 'http://parity.test/app/final',
			resource : 0
		}
	);

	return 0;
}
