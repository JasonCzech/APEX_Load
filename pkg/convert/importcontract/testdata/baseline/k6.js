import http from 'k6/http';

export default function () {
  http.post(
    'https://api.contract.test/orders',
    '{"sku":"A-1"}',
    {headers: {'Content-Type': 'application/json'}}
  );
}
