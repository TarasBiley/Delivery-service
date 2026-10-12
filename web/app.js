'use strict';
const $ = id => document.getElementById(id);
let page = 1, pages = 0, listVersion = 0, pollVersion = 0;
let pendingRegistration = JSON.parse(sessionStorage.getItem('delivery-registration') || 'null');
function notice(message, error = false) { $('notice').hidden = false; $('notice').className = error ? 'error' : ''; $('notice').textContent = message; }
async function api(path, options = {}) {
 const response = await fetch(path, {credentials: 'same-origin', ...options, headers: {'Content-Type': 'application/json', ...options.headers}});
 const body = await response.json();
 if (!response.ok) {
  const detail = body.error;
  const message = detail.code === 'CARRIER_ALREADY_ASSIGNED' ? 'Перевозчик уже назначен. Повторное назначение запрещено.' : detail.message;
  const fields = Object.entries(detail.details).map(([key, value]) => `${key}: ${typeof value === 'string' ? value : JSON.stringify(value)}`).join('\n');
  const error = new Error(message + (fields ? '\n' + fields : '')); error.status = response.status; throw error;
 }
 return body;
}
function cell(row, text) { const td = document.createElement('td'); td.textContent = text; row.append(td); return td; }
function empty(table, columns, message) { table.replaceChildren(); const row = table.insertRow(); const td = cell(row, message); td.colSpan = columns; }
async function loadTypes() {
 const data = await api('/api/v1/package-types');
 const types = Array.isArray(data) ? data : data.data;
 $('create-type').replaceChildren(new Option('Выберите тип', ''));
 for (const type of types) { $('create-type').add(new Option(type.name, String(type.id))); $('filter-type').add(new Option(type.name, String(type.id))); }
}
async function loadPackages() {
 const version = ++listVersion;
 const query = new URLSearchParams({page: String(page), limit: '10'});
 if ($('filter-type').value) query.set('package_type_id', $('filter-type').value);
 if ($('filter-calculated').value) query.set('calculated', $('filter-calculated').value);
 const result = await api('/api/v1/packages?' + query);
 if (version !== listVersion) return;
 pages = result.pagination.total_pages;
 if (page > Math.max(1, pages)) { page = Math.max(1, pages); return loadPackages(); }
 $('packages').replaceChildren();
 for (const item of result.data) {
  const row = $('packages').insertRow(); const name = cell(row, item.name); const id = document.createElement('small'); id.textContent = item.package_id; name.append(id);
  cell(row, item.weight_kg); cell(row, item.package_type.name); cell(row, item.content_value_usd);
  cell(row, item.delivery_cost_rub === null ? 'Не рассчитано' : String(item.delivery_cost_rub));
  cell(row, item.usd_to_rub_rate ?? '—');
  cell(row, item.status === 'calculated' ? 'Рассчитано' : 'Обрабатывается');
  const carrier = cell(row, item.carrier_id === null ? '' : String(item.carrier_id));
  if (item.carrier_id === null) {
   const form = document.createElement('form'); form.className = 'carrier-form';
   const input = document.createElement('input'); input.type = 'text'; input.inputMode = 'numeric'; input.pattern = '[1-9][0-9]*'; input.required = true; input.placeholder = 'Carrier ID'; input.setAttribute('aria-label', 'ID перевозчика для ' + item.name);
   const button = document.createElement('button'); button.textContent = 'Назначить'; form.append(input, button); carrier.append(form);
   form.addEventListener('submit', async event => {
    event.preventDefault();
    // Keep BIGINT exact: insert only a validated integer token into JSON.
    if (!/^[1-9][0-9]*$/.test(input.value) || BigInt(input.value) > 9223372036854775807n) { notice('carrier_id: положительное целое до 9223372036854775807', true); return; }
    button.disabled = true;
    try { await api(`/api/v1/packages/${item.package_id}/assign-carrier`, {method: 'POST', body: '{"carrier_id":' + input.value + '}'}); notice('Перевозчик назначен.'); await loadPackages(); }
    catch (error) { notice(error.message, true); if (error.status === 409) await loadPackages().catch(e => notice(e.message, true)); }
    finally { button.disabled = false; }
   });
  }
 }
 if (!result.data.length) empty($('packages'), 8, 'В этой сессии нет посылок с выбранными фильтрами.');
 $('page-info').textContent = `Страница ${page} из ${Math.max(1, pages)} · Всего ${result.pagination.total}`;
 $('prev').disabled = page <= 1; $('next').disabled = page >= pages;
}
async function pollPackage(id, version) {
 for (let attempt = 0; attempt < 12; attempt++) {
  await new Promise(resolve => setTimeout(resolve, 1500));
  if (version !== pollVersion) return;
  try {
   const item = await api('/api/v1/packages/' + id);
   if (item.status === 'calculated') { await loadPackages(); notice('Посылка рассчитана. Доставка: ' + item.delivery_cost_rub + ' RUB.'); return; }
  } catch (error) { if (error.status !== 404) { notice(error.message, true); return; } }
 }
 await loadPackages(); notice('Посылка принята, но обработка ещё не завершена. Обновите список позже.');
}
$('register').addEventListener('submit', async event => {
 event.preventDefault(); const button = event.currentTarget.querySelector('button'); button.disabled = true;
 const values = new FormData(event.currentTarget);
 try {
  const payload = JSON.stringify({name: values.get('name'), weight_kg: Number(values.get('weight_kg')), package_type_id: Number(values.get('package_type_id')), content_value_usd: Number(values.get('content_value_usd'))});
  if (!pendingRegistration || pendingRegistration.payload !== payload) {
   pendingRegistration = {key: crypto.randomUUID(), payload};
   sessionStorage.setItem('delivery-registration', JSON.stringify(pendingRegistration));
  }
  // Keep the same key/payload after a lost response; reserve a new key after 201.
  const result = await api('/api/v1/packages', {method: 'POST', body: pendingRegistration.payload, headers: {'Idempotency-Key': pendingRegistration.key}});
  pendingRegistration = null; sessionStorage.removeItem('delivery-registration');
  notice('Посылка принята в обработку. ID: ' + result.package_id); page = 1; await loadPackages().catch(error => notice('Посылка принята. Не удалось обновить список: ' + error.message, true));
  pollPackage(result.package_id, ++pollVersion).catch(error => notice(error.message, true));
 } catch (error) { notice(error.message, true); } finally { button.disabled = false; }
});
$('filters').addEventListener('submit', event => { event.preventDefault(); page = 1; loadPackages().catch(error => notice(error.message, true)); });
$('refresh').onclick = () => loadPackages().catch(error => notice(error.message, true));
$('prev').onclick = () => { if (page > 1) { page--; loadPackages().catch(error => notice(error.message, true)); } };
$('next').onclick = () => { if (page < pages) { page++; loadPackages().catch(error => notice(error.message, true)); } };
$('analytics-date').value = new Date().toISOString().slice(0, 10);
$('analytics').addEventListener('submit', async event => {
 event.preventDefault(); const button = event.currentTarget.querySelector('button'); button.disabled = true;
 try {
  const result = await api('/api/v1/analytics/daily-sum?date=' + encodeURIComponent($('analytics-date').value)); $('daily').replaceChildren();
  for (const item of result.data) { const row = $('daily').insertRow(); cell(row, item.package_type_name); cell(row, item.package_count); cell(row, item.delivery_cost_rub); }
  if (!result.data.length) empty($('daily'), 3, 'За этот день расчётов нет.');
 } catch (error) { notice(error.message, true); } finally { button.disabled = false; }
});
Promise.all([loadTypes(), loadPackages()]).catch(error => notice(error.message, true));
