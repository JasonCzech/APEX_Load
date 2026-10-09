function Action()
{
	web.regSaveParamEx(
		{
			paramName : 'SessionID',
			lb : 'name="session" value="',
			rb : '"',
			searchFilters : { scope : 'Body' }
		}
	);

	web.url(
		{
			name : 'home',
			url : 'http://parity.test/app/home',
			resource : 0,
			recContentType : 'text/html',
			mode : 'HTTP'
		}
	);

	lr.startTransaction('Login');

	web.submitData(
		{
			name : 'login',
			action : 'http://parity.test/app/login',
			method : 'POST',
			recContentType : 'text/html',
			mode : 'HTTP',
			itemData : [
				{ name : 'user', value : 'jojo' },
				{ name : 'session', value : '{SessionID}' }
			]
		}
	);

	lr.endTransaction('Login', lr.AUTO);

	lr.thinkTime(2);

	web.customRequest(
		{
			name : 'orders',
			url : 'http://parity.test/api/orders?session={SessionID}',
			method : 'POST',
			encType : 'application/json',
			body : '{"item":"widget","qty":2}'
		}
	);

	return 0;
}
