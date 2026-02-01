# Wildberries API: Проблема с warehouseID и offices

**Дата:** 2026-01-26
**Проблема:** ID складов из деталей поставки не совпадают с ID из списка offices

---

## 🔴 В чём проблема

Вы вызываете два метода:

**1. Детали поставки:**
```http
GET https://supplies-api.wildberries.ru/api/v1/supplies/12345
```
```json
{
  "warehouseID": 507,
  "warehouseName": "Коледино"
}
```

**2. Список offices:**
```http
GET https://marketplace-api.wildberries.ru/api/v3/offices
```
```json
[
  {
    "id": 15,           // ← НЕ совпадает с 507!
    "name": "Коледино"
  }
]
```

**Результат:** `warehouseID: 507` ≠ `id: 15` — нет совпадений вообще.

---

## ✅ Решение

Используйте **другой endpoint** для получения списка складов:

```http
GET https://supplies-api.wildberries.ru/api/v1/warehouses
```

**Ответ:**
```json
[
  {
    "ID": 507,          // ← Совпадает с warehouseID! ✅
    "name": "Коледино",
    "address": "Гомель, Могилёвская улица 1/А",
    "workTime": "24/7"
  }
]
```

---

## 📊 Сравнение endpoints

| Endpoint | URL | Возвращает | Для чего |
|----------|-----|------------|----------|
| ❌ **Wrong** | `/api/v3/offices` | `id: 15` | Seller Warehouses (офисы привязки) |
| ✅ **Correct** | `/api/v1/warehouses` | `ID: 507` | Склады WB для поставок |

---

## 💻 Пример кода

### Python (requests)

```python
import requests

headers = {"Authorization": "YOUR_API_KEY"}

# ❌ Неправильно - не работает для поставок
response = requests.get(
    "https://marketplace-api.wildberries.ru/api/v3/offices",
    headers=headers
)
offices = response.json()  # id: 15

# ✅ Правильно - работает для поставок
response = requests.get(
    "https://supplies-api.wildberries.ru/api/v1/warehouses",
    headers=headers
)
warehouses = response.json()  # ID: 507

# Теперь можно мэтчить
supply_warehouse_id = 507
warehouse = next((w for w in warehouses if w["ID"] == supply_warehouse_id), None)
print(warehouse["name"])  # "Коледино"
```

### JavaScript (fetch)

```javascript
const headers = { "Authorization": "YOUR_API_KEY" };

// ❌ Неправильно
const offices = await fetch(
  "https://marketplace-api.wildberries.ru/api/v3/offices",
  { headers }
).then(r => r.json()); // id: 15

// ✅ Правильно
const warehouses = await fetch(
  "https://supplies-api.wildberries.ru/api/v1/warehouses",
  { headers }
).then(r => r.json()); // ID: 507

// Мэтчинг
const supplyWarehouseId = 507;
const warehouse = warehouses.find(w => w.ID === supplyWarehouseId);
console.log(warehouse.name); // "Коледино"
```

### Go

```go
// ❌ Неправильно
resp, _ := http.Get("https://marketplace-api.wildberries.ru/api/v3/offices")
// возвращает officeId, не warehouseID

// ✅ Правильно
resp, _ := http.Get("https://supplies-api.wildberries.ru/api/v1/warehouses")

type Warehouse struct {
    ID       int    `json:"ID"`    // Совпадает с warehouseID из поставок
    Name     string `json:"name"`
    Address  string `json:"address"`
}

var warehouses []Warehouse
json.NewDecoder(resp.Body).Decode(&warehouses)

// Мэтчинг
supplyWarehouseID := 507
for _, w := range warehouses {
    if w.ID == supplyWarehouseID {
        fmt.Println(w.Name) // "Коледино"
        break
    }
}
```

### PHP

```php
$curl = curl_init();
curl_setopt_array($curl, [
    CURLOPT_URL => "https://supplies-api.wildberries.ru/api/v1/warehouses",
    CURLOPT_HTTPHEADER => ["Authorization: YOUR_API_KEY"],
    CURLOPT_RETURNTRANSFER => true,
]);

$response = curl_exec($curl);
$warehouses = json_decode($response, true);

// Мэтчинг
$supplyWarehouseId = 507;
$warehouse = null;
foreach ($warehouses as $w) {
    if ($w['ID'] === $supplyWarehouseId) {
        $warehouse = $w;
        break;
    }
}
echo $warehouse['name']; // "Коледино"
```

### C# (HttpClient)

```csharp
using System;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text.Json;
using System.Threading.Tasks;

public class Warehouse
{
    public int ID { get; set; }      // Совпадает с warehouseID из поставок
    public string Name { get; set; }
    public string Address { get; set; }
}

public class WBApiClient
{
    private readonly HttpClient _httpClient = new HttpClient();

    public WBApiClient(string apiKey)
    {
        _httpClient.DefaultRequestHeaders.Add("Authorization", apiKey);
    }

    public async Task<Warehouse[]> GetWarehousesAsync()
    {
        var response = await _httpClient.GetStringAsync(
            "https://supplies-api.wildberries.ru/api/v1/warehouses"
        );
        return JsonSerializer.Deserialize<Warehouse[]>(response);
    }

    public Warehouse FindWarehouse(Warehouse[] warehouses, int supplyWarehouseId)
    {
        foreach (var w in warehouses)
        {
            if (w.ID == supplyWarehouseId)
                return w;
        }
        return null;
    }
}

// Использование
var client = new WBApiClient("YOUR_API_KEY");
var warehouses = await client.GetWarehousesAsync();

var supplyWarehouseId = 507;
var warehouse = client.FindWarehouse(warehouses, supplyWarehouseId);
Console.WriteLine(warehouse.Name); // "Коледино"
```

**Или короче с LINQ:**

```csharp
using System.Linq;

// ...
var supplyWarehouseId = 507;
var warehouse = warehouses.FirstOrDefault(w => w.ID == supplyWarehouseId);
Console.WriteLine(warehouse?.Name); // "Коледино"
```

---

## 🧪 Тест (curl)

```bash
# Проверить, что warehouseID из поставки есть в списке складов
curl -H "Authorization: YOUR_API_KEY" \
  "https://supplies-api.wildberries.ru/api/v1/warehouses" | \
  jq '.[] | select(.ID == 507)'

# Результат:
# {
#   "ID": 507,
#   "name": "Коледино",
#   ...
# }
```

---

## 📝 Текст для поддержки WB

Если нужна официальная информация:

> **Тема:** Несовпадение ID складов в API
>
> Здравствуйте!
>
> Используем два метода:
> 1. `GET /api/v1/supplies/{ID}` — детали поставки, поле `warehouseID: 507`
> 2. `GET /api/v3/offices` — список offices, поле `id: 15`
>
> Эти ID не совпадают. Нашли решение через `/api/v1/warehouses`, но хотим уточнить:
> - Есть ли官方ный способ связать officeId с warehouseID?
> - Можно ли добавить поле warehouseId в ответ `/api/v3/offices`?

---

## 🔗 Полезные ссылки

| Документация | Ссылка |
|--------------|--------|
| FBW Supplies API | https://dev.wildberries.ru/openapi/orders-fbw |
| Seller Warehouses API | https://dev.wildberries.ru/openapi/work-with-products |

---

**Коротко:** Замените `/api/v3/offices` на `/api/v1/warehouses` — и всё заработает! 🎯
