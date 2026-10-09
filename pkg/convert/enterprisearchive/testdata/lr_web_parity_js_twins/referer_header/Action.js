function Action()
{
	web.url(
		{
			name : 'home',
			url : 'http://parity.test/app/home',
			resource : 0,
			referer : '',
			mode : 'HTTP'
		}
	);

	web.submitData(
		{
			name : 'search',
			action : 'http://parity.test/app/search',
			method : 'POST',
			referer : 'http://parity.test/app/home',
			mode : 'HTTP',
			itemData : [
				{ name : 'q', value : 'widgets' }
			]
		}
	);

	web.customRequest(
		{
			name : 'results',
			url : 'http://parity.test/api/results',
			method : 'GET',
			referer : 'http://parity.test/app/search?q=widgets'
		}
	);

	return 0;
}
