# excel-ql 使用指南

`excel-ql` 使用 Go 链式 API 查询 Excel 工作表，支持筛选、映射、排序、分页、分组聚合、左右连接及集合运算。所有操作均为只读。

## 1. 准备环境与数据

当前模块要求 **Go 1.27 或更新版本**。以下命令在项目根目录执行。

准备一个 `sales.xlsx` 文件，其中包含名为 `Sales` 的工作表：

| id | team | amount | note |
| --- | --- | --- | --- |
| 001 | 研发 | 120 | 加急 |
| 002 | 研发 | 80 | |
| 003 | 销售 | 50 | 常规 |
| 004 | 销售 | 200 | 加急 |

第一行填写表头，第二行开始填写数据。`id` 列需要保留前导零时，应在 Excel 中存储为文本。

先运行已有的命令行示例：

```sh
go run ./examples/basic -file sales.xlsx -sheet Sales
```

选择列并分页：

```sh
go run ./examples/basic -file sales.xlsx -sheet Sales -columns id,team,amount -offset 1 -limit 2
```

| 参数 | 含义 | 默认值 |
| --- | --- | --- |
| `-file` | 工作簿路径，必填 | 无 |
| `-sheet` | 工作表名称 | 第一个工作表 |
| `-columns` | 逗号分隔的输出列名 | 全部列 |
| `-offset` | 跳过的数据行数 | `0` |
| `-limit` | 最多输出的数据行数 | `10` |

命令行示例按源数据顺序输出。需要筛选或排序时，可使用下面的 Go API。

## 2. 完整 Go 示例

以下程序查询金额大于 60 的记录，按金额降序输出。可以将程序保存到当前模块的新子目录，例如 `examples/custom/main.go`，然后执行 `go run ./examples/custom`。工作簿路径相对于运行命令时的目录。

```go
package main

import (
	"errors"
	"fmt"
	"log"

	ql "github.com/23jdd/excel-ql"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	db, err := ql.Open("sales.xlsx")
	if err != nil {
		return err
	}

	rows, queryErr := db.From("Sales").
		Select([]string{"id", "team", "amount"}).
		Where(ql.Number("amount").Gt(60)).
		OrderBy(ql.NumericDesc("amount"), ql.Asc("id")).
		Offset(0).
		Limit(10).
		Query()

	// Query 已生成结果，关闭文件后仍可遍历。
	if err := errors.Join(queryErr, db.Close()); err != nil {
		return err
	}
	for row := range rows {
		fmt.Println(row.Values)
	}
	return nil
}
```

使用上面的示例数据，输出为：

```text
[004 销售 200]
[001 研发 120]
[002 研发 80]
```

后续代码片段均假定已导入 `ql "github.com/23jdd/excel-ql"`，且 `db` 已成功打开、尚未关闭。每段查询独立使用；调用 `Query()` 后先检查 `err`，再遍历结果。

## 3. 工作表与列名识别

`db.Sheets()` 返回工作表名称列表，`db.From("Sales")` 按名称精确选择工作表。

列名来自 **第一行单元格的内容**。例如 A1 为 `id`，B1 为 `team`，查询中就使用 `id`、`team`。

- 表头会去除首尾空白：`" team "` 识别为 `"team"`。
- 列名区分大小写，`team` 和 `Team` 是两个名称。
- 空表头、重复表头、数据超出表头范围，以及引用不存在的列，均返回错误。
- 不调用 `Select` 时输出当前阶段的全部列；普通查询按表头顺序输出。
- `Select` 按传入顺序输出列，多次调用会追加列；空选择列表、重复输出列名会报错。
- 当前表头固定在第一行，未提供通过 Excel 列字母或其他行号选择列的接口。

```go
rows, err := db.From("Sales").
	Select([]string{"amount", "id"}).
	Query()
```

这里每行的输出顺序为 `amount`、`id`。

调用 `As("s")` 后，源字段都使用别名前缀，即使没有连接其他工作表也是如此：

```go
rows, err := db.From("Sales").As("s").
	Select([]string{"s.id", "s.amount"}).
	Where(ql.Number("s.amount").Gt(100)).
	Query()
```

## 4. 读取查询结果

`Query()` 返回 `(iter.Seq[ql.Row], error)`。成功时返回可重复遍历的序列，无匹配行时遍历零次；失败时返回 `nil` 序列和错误。

`Row` 包含按位置对应的 `Columns []string` 和 `Values []any`：

```go
for row := range rows {
	fmt.Println(row.Columns, row.Values)
	value, err := row.Get("amount")
	if err != nil {
		return err
	}
	fmt.Println(value)
}
```

`Get` 返回 `nil, nil` 表示存在这个列但值为空；列名不存在时返回错误。

原始字段为 `string` 或 `nil`。`Count` 返回 `int64`，内置数值聚合返回 `float64` 或 `nil`，自定义映射和聚合可返回其他类型。

`Query()` 返回前会完成读取和计算，因此结果不依赖文件句柄。关闭文件、提前退出遍历或重新遍历，都不会改变已经生成的结果；修改查询构建器也不会改变之前的序列。

## 5. 条件筛选

文本比较使用 `Col`，数值比较使用 `Number`：

```go
rows, err := db.From("Sales").
	Where(
		ql.Col("team").Eq("研发"),
		ql.Number("amount").Gte(100),
	).
	Query()
```

| 方法 | 含义 |
| --- | --- |
| `Eq(value)` | 等于 |
| `Ne(value)` | 不等于 |
| `Gt(value)` | 大于 |
| `Gte(value)` | 大于等于 |
| `Lt(value)` | 小于 |
| `Lte(value)` | 小于等于 |
| `IsNull()` | 空值 |
| `IsNotNull()` | 非空值 |

`Col` 按字符串比较，例如 `"10"` 会排在 `"2"` 前面。`Number` 显式进行数值转换，非法数字、`NaN` 和无穷大会导致查询失败。

多个 `Where` 参数及多次 `Where` 调用按 AND 组合。通过 `And`、`Or`、`Not` 表达嵌套逻辑：

```go
rows, err := db.From("Sales").
	Where(ql.Not(ql.Or(
		ql.And(
			ql.Col("team").Eq("销售"),
			ql.Number("amount").Lt(100),
		),
		ql.Col("note").IsNull(),
	))).
	Query()
```

`And`、`Or` 从左到右短路求值，但所有已声明的列名仍会被校验。空条件、空的 `Where()`、`And()` 或 `Or()` 会报错。

空单元格和空字符串统一为 `nil`，只含空白字符的文本仍是有效值。普通比较遇到空值返回 `false`，`Not` 对此直接取反为 `true`。如需排除空值，应显式添加 `IsNotNull()`。

## 6. 排序与分页

```go
rows, err := db.From("Sales").
	Select([]string{"id", "team", "amount"}).
	OrderBy(ql.NumericDesc("amount"), ql.Asc("id")).
	Offset(1).
	Limit(2).
	Query()
```

这个查询按金额降序、再按 `id` 升序排列，跳过第一行，返回接下来的两行。

| 方法 | 行为 |
| --- | --- |
| `Asc(column)` / `Desc(column)` | 按字符串升序 / 降序 |
| `NumericAsc(column)` / `NumericDesc(column)` | 显式转换后按数值升序 / 降序 |
| `Offset(n)` | 跳过排序后的前 n 行 |
| `Limit(n)` | 限制返回行数 |

- 排序稳定，相同排序键保持原有顺序；多个键按声明顺序比较。
- 排序只能引用最终输出的列，空值无论升降序都放在最后。
- `Offset` 默认为 0，达到或超过结果行数时返回空结果。
- 不设置 `Limit` 时不限制行数，`Limit(0)` 返回空结果。
- `Offset`、`Limit` 不接受负数，错误统一由 `Query()` 返回。
- 多次有效调用 `Offset` 或 `Limit`，以各自最后一次为准。
- 执行顺序固定为先排序、再 `Offset`、最后 `Limit`，与链式方法书写顺序无关。

使用页码时，可在校验页码和每页数量后计算偏移量：

```go
page, pageSize := 2, 10 // page 从 1 开始
rows, err := db.From("Sales").
	OrderBy(ql.Asc("id")).
	Offset((page - 1) * pageSize).
	Limit(pageSize).
	Query()
```

分页时应指定可确定顺序的排序键，例如唯一的 `id`。

## 7. 分组与聚合

```go
rows, err := db.From("Sales").
	GroupBy("team").
	Aggregate(
		ql.Sum("amount").As("total"),
		ql.Avg("amount").As("average"),
		ql.Count().As("orders"),
	).
	Having(ql.Number("total").Gt(100)).
	Select([]string{"team", "total", "average", "orders"}).
	OrderBy(ql.NumericDesc("total")).
	Query()
```

使用示例数据，结果为：

| team | total | average | orders |
| --- | --- | --- | --- |
| 销售 | 250 | 125 | 2 |
| 研发 | 200 | 100 | 2 |

| 聚合函数 | 行为 | 返回类型 |
| --- | --- | --- |
| `Count()` | 统计行数 | `int64` |
| `Count("amount")` | 统计指定列非空值数量 | `int64` |
| `Sum("amount")` | 求和 | `float64` 或 `nil` |
| `Avg("amount")` | 平均值 | `float64` 或 `nil` |
| `Max("amount")` | 数值最大值 | `float64` 或 `nil` |
| `Min("amount")` | 数值最小值 | `float64` 或 `nil` |

- 调用数值聚合即显式选择数值转换；空值被忽略，非空但无法转换的值导致错误。
- `.As("total")` 指定聚合输出名称，未指定时使用 `sum(amount)`、`count(*)` 等名称。
- `Where` 在分组前过滤原始行；`Having` 在分组后过滤，只能访问分组键及聚合输出。
- 聚合查询中，普通输出列必须属于分组键。未调用 `Select` 时，依次输出分组键和聚合声明。
- 无分组的空输入仍产生一行聚合结果：计数为 0，数值聚合为 `nil`；有分组的空输入产生零行。
- `Sum`、`Avg`、`Max`、`Min` 使用 `float64`。需要精确十进制或文本、日期聚合时，请提供自定义聚合。

## 8. 左连接与右连接

假设同一个工作簿另有 `Users` 和 `Orders` 工作表：

- `Users` 表头为 `id`、`name`。
- `Orders` 表头为 `user_id`、`amount`。

左连接保留全部用户，没有订单的用户，其订单字段为 `nil`：

```go
rows, err := db.From("Users").As("u").
	LeftJoin(db.From("Orders").As("o"), ql.On("id", "user_id")).
	Select([]string{"u.id", "u.name", "o.amount"}).
	Query()
```

将 `LeftJoin` 替换为 `RightJoin`，则保留全部订单，未匹配的用户字段为 `nil`。

连接规则：

- 左右来源均须设置唯一别名；输出字段使用 `别名.列名`。
- `On` 可使用裸列名，也可使用完整列名，例如 `ql.On("u.id", "o.user_id")`。
- 支持多个连接键；例如两表都有 `tenant_id` 时，可追加 `ql.On("tenant_id", "tenant_id")`，各键同时相等才匹配。
- 按类型和值精确匹配，不自动转换数字；例如文本 `"001"` 与 `"1"` 不匹配。任一键为空值时不匹配。
- 重复键保留全部匹配组合：左侧两行与右侧三行具有相同键时，生成六行。
- 默认先按保留侧源行顺序，再按另一侧匹配行顺序输出。

## 9. 集合运算

假设 `Current` 和 `Archive` 都有 `id` 列：

```go
rows, err := db.From("Current").Select([]string{"id"}).
	Union(db.From("Archive").Select([]string{"id"})).
	Query()
```

| 方法 | 行为 |
| --- | --- |
| `Union(other)` | 并集，保留两侧出现的行 |
| `Intersect(other)` | 交集，保留两侧都出现的行 |
| `Except(other)` | 差集，保留左侧出现、右侧未出现的行 |

三者默认去重，要求两侧输出列数、列名及顺序完全一致，按整行比较。空值与空值视为相等，字符串 `"1"`、`int64(1)` 和 `float64(1)` 互不相等。

并集先保留左侧首次出现的行，再追加右侧新行；交集和差集保留左侧首次出现的顺序。

集合运算在每个查询的排序和分页之后执行。即使写成 `.Union(other).Offset(10).Limit(10)`，分页仍然作用于左侧输入分支；右侧需要单独设置分页。多个集合运算按调用顺序执行。

## 10. 自定义函数

自定义函数可返回错误，错误最终由 `Query()` 返回。条件、映射和聚合的依赖列必须声明完整，输入为空时也会校验这些列名。

### 自定义条件

以下片段额外需要导入 `strings`：

```go
hasUrgentNote := ql.Predicate([]string{"note"}, func(row ql.Row) (bool, error) {
	value, err := row.Get("note")
	if err != nil || value == nil {
		return false, err
	}
	return strings.Contains(value.(string), "加急"), nil
})
rows, err := db.From("Sales").Where(hasUrgentNote).Query()
```

### 自定义列筛选与映射

```go
rows, err := db.From("Sales").
	SelectFunc(func(column string) (bool, error) {
		return column != "note", nil
	}).
	Map(ql.Map("label", []string{"id", "team"}, func(row ql.Row) (any, error) {
		id, err := row.Get("id")
		if err != nil {
			return nil, err
		}
		team, err := row.Get("team")
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("%v/%v", id, team), nil
	})).
	Query()
```

`SelectFunc` 筛选 `Select` 指定的列，未调用 `Select` 时筛选全部列。多个筛选器按 AND 组合；筛选后没有输出列且未添加映射时返回错误。

映射列追加在已选列之后，名称不能与其他输出列重复。映射可以访问投影前的字段，包括未选出的字段；同一查询中的映射之间不能互相依赖。若只需映射列，可让 `SelectFunc` 始终返回 `false`。

### 自定义排序

以下片段额外需要导入 `strings`：

```go
rows, err := db.From("Sales").
	OrderBy(ql.CustomOrder("team", false, func(a, b any) (int, error) {
		return strings.Compare(strings.ToLower(a.(string)), strings.ToLower(b.(string))), nil
	})).
	Query()
```

第二个参数控制是否降序。比较函数只接收非空值，应返回负数、零或正数，表示小于、相等、大于；空值仍统一排在最后。

### 自定义聚合

以下片段额外需要导入 `strings`：

```go
notes := ql.CustomAggregate("notes", []string{"note"}, func(group []ql.Row) (any, error) {
	var values []string
	for _, row := range group {
		value, err := row.Get("note")
		if err != nil {
			return nil, err
		}
		if value != nil {
			values = append(values, value.(string))
		}
	}
	return strings.Join(values, ";"), nil
})
rows, err := db.From("Sales").GroupBy("team").Aggregate(notes).Query()
```

无分组且输入为空时，回调仍会接收一个空切片，需要自行定义此时的返回值。

### 自定义集合相等规则与运算

`Union`、`Intersect`、`Except` 可额外传入一个 `ql.EqualFunc`，类型为 `func(ql.Row, ql.Row) (bool, error)`。自定义相等规则应满足自反、对称及传递性。映射结果包含内置比较不支持的类型时，必须提供此规则。

使用 `Combine` 可以实现保留重复行的拼接：

```go
rows, err := db.From("Current").Select([]string{"id"}).
	Combine(db.From("Archive").Select([]string{"id"}),
		func(left, right []ql.Row) ([]ql.Row, error) {
			return append(left, right...), nil
		}).
	Query()
```

两侧输入及输出必须保持相同的列名、列顺序和字段数量。回调接收独立的列和值切片，自定义引用类型的深复制由调用方负责。

## 11. 执行顺序与错误处理

执行顺序固定为：

```text
读取数据 → 连接 → Where → 分组/聚合 → Having
        → Select/映射 → OrderBy → Offset → Limit → 集合运算
```

链式方法的书写位置不会改变阶段顺序。例如，`Where` 总在聚合之前执行，`Offset` 总在 `Limit` 之前执行。

- `Open` 直接返回文件打开错误，`Close` 直接返回资源释放错误。
- 链式方法记录参数错误，读取、校验、计算和回调错误统一由 `Query()` 返回。
- 已记录的参数错误不会被后续有效调用清除。例如 `.Offset(-1).Offset(0)` 仍然报错，应重新创建语句。
- 失败时不会返回部分结果。包装后的底层错误可用 `errors.Is`、`errors.As` 检查。
- `Stmt` 是可变构建器，复用并修改同一个指针会影响后续查询，不能并发修改。需要独立查询时分别调用 `From`。

## 12. 数据边界与限制

- 当前使用内存快照，`iter.Seq` 遍历的是已经计算好的结果，不会按需从磁盘逐行读取。
- 只读取 Excelize 支持的 OOXML 工作簿，不支持旧版二进制 `.xls`。
- 原始值按未应用显示格式的字符串读取，不自动推断日期或布尔值。
- 公式读取文件中保存的缓存结果，不重新计算；缺少缓存结果时报错。
- 合并区域仅保留左上角单元格值，其余位置为空值；整行为空的数据行跳过。
- `Close` 可重复调用；关闭后不能再查询文件，但之前生成的结果仍然可用。

项目定位和完整语义约定见 [README.md](README.md)，可运行的命令行程序见 [examples/basic/main.go](examples/basic/main.go)。
