function Action()
{
	web.url(
		{
			name : 'home',
			url : 'http://parity.test/app/home',
			resource : 0,
			recContentType : 'text/html',
			mode : 'HTML'
		}
	);

	web.regFind({ text : 'Welcome' });

	web.submitData(
		{
			name : 'login',
			action : 'http://parity.test/app/login',
			method : 'POST',
			itemData : [
				{ name : 'user', value : 'jojo' },
				{ name : 'password', value : 'bean' }
			]
		}
	);

	return 0;
}
