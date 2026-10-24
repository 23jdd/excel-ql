# excel-ql

使用 Go 链式 API，以类似 SQL 的方式查询 Excel 文件，无需编写 SQL 字符串或 `.sql` 文件。

项目专注于只读查询，不提供 `INSERT`、`UPDATE` 或 `DELETE` 操作。

完整使用说明与示例见 [Usage.md](Usage.md)，包括列名识别、条件组合、分页、聚合、连接及自定义函数。

## 当前状态

首版查询功能已实现，支持筛选、映射、排序、分组聚合、左右连接及集合运算。查询使用内存快照，成功后返回可重复遍历的 `iter.Seq[Row]`。

| API | 用途 | 当前进度 |
| --- | --- | --- |
| `Open(path)` | 打开 Excel 文件 | 已实现 |
| `Sheets()` | 获取工作表名称列表 | 已实现 |
| `From(sheet)`、`As(alias)` | 指定工作表和来源别名 | 已实现 |
| `Select(columns)`、`SelectFunc(fn)`、`Map(...)` | 列选择、列筛选及映射 | 已实现 |
| `Where(...)`、`And`、`Or`、`Not` | 条件筛选及嵌套组合 | 已实现 |
| `OrderBy(...)`、`Offset(n)`、`Limit(n)` | 稳定排序、跳过行及行数限制 | 已实现 |
| `GroupBy(...)`、`Aggregate(...)`、`Having(...)` | 分组、聚合及分组筛选 | 已实现 |
| `LeftJoin(...)`、`RightJoin(...)` | 单键或复合键左右连接 | 已实现 |
| `Union(...)`、`Intersect(...)`、`Except(...)`、`Combine(...)` | 集合运算及自定义运算 | 已实现 |
| `Query()` | 执行查询，统一返回结果或错误 | 已实现 |
| `Close()` | 释放文件资源，重复关闭安全 | 已实现 |

## 快速开始

需要 Go 1.27 或更新版本。克隆项目后可运行测试和命令行示例：

```sh
go test ./...
go run ./examples/basic -file sales.xlsx -sheet Sales -columns team,amount -limit 10
go run ./examples/basic -file sales.xlsx -sheet Sales -offset 10 -limit 10
```

示例读取已有的工作簿，不修改文件；省略 `-sheet` 时使用第一个工作表，省略 `-columns` 时输出全部列。

分页使用 `Offset(n)` 跳过前 n 行，再使用 `Limit(n)` 限制返回数量。例如 `OrderBy(ql.Asc("id")).Offset(10).Limit(10)` 返回排序后的第 11 至 20 行。命令行对应 `-offset 10 -limit 10`，默认偏移量为 0。

以下函数查询包含 `team` 和 `amount` 表头的 `Sales` 工作表，按团队汇总金额，保留总额大于 100 的团队：

```go
package example

import (
    "errors"
    "fmt"

    ql "github.com/23jdd/excel-ql"
)

func printSales(path string) error {
    db, err := ql.Open(path)
    if err != nil {
        return err
    }

    rows, queryErr := db.From("Sales").
        Where(ql.Col("team").IsNotNull()).
        GroupBy("team").
        Aggregate(ql.Sum("amount").As("total"), ql.Count().As("orders")).
        Having(ql.Number("total").Gt(100)).
        Select([]string{"team", "total", "orders"}).
        OrderBy(ql.NumericDesc("total")).
        Limit(10).
        Query()

    if err := errors.Join(queryErr, db.Close()); err != nil {
        return err
    }
    for row := range rows {
        fmt.Println(row.Columns, row.Values)
    }
    return nil
}
```

查询完成后即可关闭文件，结果仍然可用。`Row.Get("total")` 可按列名访问值，列名不存在时返回错误。

## 已支持功能

### 基础查询

- 文件管理：`Open`、`Close`。
- 工作表访问：`Sheets`、`From`。
- 语句构建与执行：`Stmt`、`Select`、`Where`、`Query`。
- 结果控制：`OrderBy`、`Offset`、`Limit`。

### 聚合与分组

- 聚合函数：`Sum`、`Count`、`Avg`、`Max`、`Min`。
- 分组：`GroupBy`。
- 分组结果筛选：`Having`。

### 连接与集合运算

- 左连接、右连接：`LeftJoin`、`RightJoin`。
- 并集、交集、差集等集合运算。

条件比较通过 `Col("name").Eq("Alice")` 等方法构建，支持 `Eq`、`Ne`、`Gt`、`Gte`、`Lt`、`Lte`。数值条件使用 `Number("amount").Gt(100)`，空值判断使用 `IsNull()` 或 `IsNotNull()`。

聚合函数交给 `Aggregate(...)` 执行，使用 `.As("name")` 指定输出列名。未指定名称时使用 `sum(amount)`、`count(*)` 等默认名称。

连接示例（左右工作表均包含 `id` 列）：

```go
rows, err := db.From("Users").As("u").
    LeftJoin(db.From("Orders").As("o"), ql.On("id", "id")).
    Select([]string{"u.id", "o.id"}).
    Query()
```

集合运算示例（两侧输出列名及顺序必须一致）：

```go
rows, err := db.From("Current").Select([]string{"id"}).
    Union(db.From("Archive").Select([]string{"id"})).
    Query()
```

## 自定义扩展

可通过以下接口传入自定义函数。所有自定义函数都可返回错误，由 `Query()` 统一返回。

- **列筛选**：`SelectFunc(func(column string) (bool, error))`。在 `Select` 指定的列上筛选，未调用 `Select` 时筛选全部列，多次调用按 AND 组合。
- **字段映射**：`Map(ql.Map(name, dependencies, func(Row) (any, error)))`。计算列追加在已选列后，依赖字段来自投影前的行，映射之间不互相依赖。可用始终返回 `false` 的列筛选器只输出映射列。
- **条件筛选**：`Where(ql.Predicate(dependencies, func(Row) (bool, error)))`。
- **排序**：`OrderBy(ql.CustomOrder(column, descending, func(a, b any) (int, error)))`。比较函数只接收非空值，返回负数、零或正数。
- **聚合**：`Aggregate(ql.CustomAggregate(name, dependencies, func([]Row) (any, error)))`。
- **集合相等判断**：`Union`、`Intersect`、`Except` 接受一个可选的 `EqualFunc`，应满足自反、对称及传递性。
- **集合运算**：`Combine(other, func(left, right []Row) ([]Row, error))`。可自定义行组合方式，输出必须保持输入列名、列顺序和字段数量。

`dependencies` 必须声明回调访问的全部列，以便在输入为空时也能检查列名。回调接收独立的列和值切片；自定义引用类型的复制由调用方负责。

## 开发信息

- 模块路径：`github.com/23jdd/excel-ql`。
- 包名：`ql`。
- Excel 文件处理依赖：`github.com/xuri/excelize/v2`。
- Go 版本要求以 `go.mod` 为准，当前声明为 `1.27`。
- 验证命令：`go test ./...`、`go vet ./...`；支持 C 编译器的环境还可运行 `go test -race ./...`。
- `Stmt` 是可变的查询构建器，不应并发修改；已生成的结果序列可独立重复遍历。

## 当前限制

- 工作簿及查询结果保存在内存中，`iter.Seq` 用于遍历已计算的结果，不代表逐行读取磁盘。
- 读取 Excelize 支持的 OOXML 工作簿，不支持旧版二进制 `.xls` 文件。
- 公式必须有已保存的缓存结果，查询不会重算公式。日期和布尔值需要自行解析。
- 数值转换和内置数值聚合使用 `float64`，不适合要求十进制精确运算的金额计算；此类需求应使用自定义聚合。

## 明确的查询约定

以下约定描述当前实现的查询行为。

### 1. 工作表、表头与列名

- `From(sheet)` 按名称选择单个工作表，名称必须精确匹配；工作表不存在时返回错误。
- 调用 `As(alias)` 后，源字段统一以 `alias.列名` 引用，即使当前查询尚未执行连接也是如此。
- 默认以第一行为表头，第二行开始为数据；空工作表返回错误，只有表头时返回空结果。
- 表头去除首尾空白后作为列名，区分大小写。空列名、重复列名均返回错误，不自动重命名。
- 查询范围覆盖表头及数据中出现的最右侧非空单元格；数据超出表头范围时，按缺失表头报错。仅有格式的空白单元格不扩展查询范围。
- 所有引用的列必须存在；列名拼写错误返回错误，不当作空值处理。
- 不调用 `Select` 时选择当前阶段的全部列：普通查询按源表头顺序输出，分组聚合查询按分组键、聚合声明顺序输出。调用后按传入顺序输出，多次调用追加列。空选择列表、重复输出列名返回错误。

### 2. 单元格与数据类型

- 原始单元格按未应用显示格式的字符串读取，不自动推断数字、布尔值或日期，保留文本中的首尾空白和前导零。
- 缺失单元格和空字符串统一表示为 `nil`。只含空白字符的字符串属于有效值；整行均为空值的数据行跳过。
- 合并区域仅左上角单元格保留值，其他位置为空值，不自动向下或向右填充。
- 公式读取文件中保存的计算结果，不在查询时重新计算；缺少缓存结果时返回错误。
- 数值比较、数值排序和数值聚合必须显式选择数值转换，或使用自定义函数。内置数值转换使用 `float64`，转换前去除首尾空白，拒绝非法数值、`NaN` 和无穷大。
- 调用 `Sum`、`Avg`、`Max`、`Min` 本身即表示显式选择数值转换；聚合计算溢出时返回错误。
- 非空值转换失败时返回错误，不跳过该行，也不替换为零；日期和布尔值由调用方显式解析。

### 3. 条件组合

- 条件以整行为输入，可访问多个字段；自定义条件使用 `Predicate` 声明所需列并提供判断函数。
- `Where(c1, c2)` 等价于 `Where(And(c1, c2))`；多次调用 `Where` 也按 AND 组合。
- 支持任意嵌套的 `And`、`Or`、`Not`，例如 `Not(Or(And(c1, c2), c3))`。
- `And`、`Or` 从左到右短路求值，`Not` 只接收一个条件。空的 `Where`、`And`、`Or` 及空条件均返回参数错误。
- 使用二值布尔逻辑：普通比较遇到空值时为 `false`，空值判断使用专门的空值条件；`Not` 对结果直接取反，因此 `Not(空值上的普通比较)` 为 `true`。
- 条件或自定义函数返回错误时，整个查询失败。

### 4. 执行顺序、排序与聚合

- 执行顺序固定为：读取数据 → 连接 → `Where` → `GroupBy` / 聚合 → `Having` → `Select` / 映射 → `OrderBy` → `Offset` → `Limit`。
- 未指定排序时保持源数据行顺序；分组结果按分组键首次出现的顺序输出。
- 排序使用稳定排序，多个排序键按声明顺序比较；默认按字符串比较，数值排序需显式转换。空值无论升降序都放在最后。
- 排序引用最终输出的列名；无效列名返回错误。多次声明排序键时按调用顺序追加。
- 未设置 `Limit` 时不限制行数，`Limit(0)` 返回空结果，负数返回错误；多次调用以最后一次为准。
- `Offset` 默认值为 0；负数返回错误，达到或超过结果行数时返回空结果。多次有效调用以最后一次为准，执行顺序始终是先 `Offset` 后 `Limit`，不受链式方法书写顺序影响。
- 分组键按原始类型和值精确比较，空值归入同一组；多个分组键按元组比较，不通过字符串拼接判断。
- `Count()` 统计行数，`Count(column)` 统计非空值数量，结果类型为 `int64`。
- 内置 `Sum`、`Avg`、`Max`、`Min` 为数值聚合，忽略空值，结果类型为 `float64`；无有效值时返回 `nil`。文本或日期聚合使用自定义函数。
- 无分组的聚合即使输入为空也返回一行；有分组且输入为空时返回零行。
- 聚合查询中，非聚合输出列必须属于分组键；`Having` 在聚合后执行，只能引用分组键及命名的聚合结果。

### 5. 查询结果与错误

- 返回签名为 `Query() (iter.Seq[Row], error)`，支持通过 Go 的 `for row := range rows` 遍历。
- `Row` 采用有序结构，包含等长的 `Columns []string` 和 `Values []any`，按投影列顺序对应，映射列追加在最后。
- 原始字段值为 `string` 或 `nil`，内置聚合结果使用上述数值类型；自定义映射可返回其他类型，但参与排序或集合比较时必须提供相应规则。
- 首版在 `Query()` 返回前完成读取、校验和计算。失败返回 `nil, error`，成功返回非 `nil` 的迭代序列；无匹配行时序列不产生元素。
- 返回的序列遍历已经生成的结果，可重复遍历且不再依赖文件句柄；提前结束遍历不影响文件资源释放。该设计会将结果保存在内存中。
- 每次遍历提供独立的列和值切片；自定义映射返回的引用类型由调用方负责复制和管理。
- 错误应包含工作表、列名、源文件行号（从 1 开始）等可用上下文；不返回部分成功结果。自定义函数需要通过错误返回值报告失败。
- `Close()` 负责释放文件资源并返回释放错误；文件关闭后再次查询返回错误，已经生成的结果仍可遍历。

### 6. 连接与集合运算

- 连接必须显式指定左右来源别名及等值连接键，支持多个键。连接键按类型和值精确匹配，不自动转换数字或忽略大小写；任一键为空值时不匹配。
- 连接后的列统一使用 `别名.列名`，避免同名列冲突；别名必须唯一。
- 重复连接键保留全部匹配组合：左侧两行、右侧三行具有同一键时，产生六行结果。
- 左连接保留左侧全部行，右连接保留右侧全部行，未匹配的一侧填充空值；结果先按保留侧源行顺序，再按另一侧匹配行顺序输出。
- 集合运算要求两侧列数、列名和列顺序一致，按整行各字段的类型和值精确比较；空值与空值视为相等，字符串 `"1"` 与数值 `1` 不相等。
- 并集、交集、差集默认去重；并集先保留左侧首次出现的行，再追加右侧新行。交集和差集按左侧首次出现的顺序输出，差集表示左侧减右侧。
- 集合运算在各自查询的 `Offset`、`Limit` 之后执行，多个集合运算按调用顺序处理。`Offset`、`Limit`、`OrderBy` 始终作用于当前查询的输入分支，不作用于合并后的整体结果，与链式方法的书写位置无关。
- 非内置可比较类型必须提供自定义相等规则；未提供时返回错误，不隐式转成字符串比较。

### 7. 错误处理

- 查询构建、参数校验、读取、转换和回调错误统一由 `Query()` 返回；链式方法记录错误并继续返回 `*Stmt`，已记录的参数错误不会被后续调用清除。
- `Open` 的文件打开错误和 `Close` 的资源释放错误由对应方法直接返回。
- 错误保留包装关系，可使用 `errors.Is`、`errors.As` 检查底层原因；查询失败时返回 `nil` 序列，不暴露部分结果。
