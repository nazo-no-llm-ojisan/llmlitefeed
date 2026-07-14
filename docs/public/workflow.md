# **設計ゲート型LLM開発ワークフロー**

## Human-Gated Decomposition and Integration Workflow

> 設計判断と実装判断を分離し、各段階で人間が承認し、作業単位を独立化してから統合する

という開発統制です。

統括LLMがいる場合も、IDE内の単一セッションだけで進める場合も、同じ骨格で動かせます。

日本語では、

> 人間承認つき設計・分解・統合ワークフロー

くらいが分かりやすいです。

---

## 全体像

```text
問題発見
  ↓
設計案を作る
  ↓
人間が設計を確認・修正
  ↓
設計を独立タスクへ分解
  ↓
人間が依存関係・境界を確認
  ↓
各タスクをLLMへ渡す
  ↓
タスク単位で実装・テスト
  ↓
統合前レビュー
  ↓
統合
  ↓
全体検証
  ↓
正本ドキュメント同期
```

重要なのは、各矢印の途中にゲートがあることです。

---

### 問題定義

最初にやるのは解決策を考えることではなく、問題の境界を固定することです。

最低限、次を言語化します。

```text
何が困っているか
誰が困っているか
現在どうなっているか
望ましい状態は何か
今回は何を扱わないか
```

ここで実装案を書き始めないのが重要です。

良い問題定義の例は、

```text
複数providerのモデル一覧JSONが異なるため、
共通Offeringへ投影する前に、
構造差と意味差を分離して正規化する必要がある。
```

悪い問題定義は、

```text
parser.tsを直す。
```

後者は解決策であって、問題ではありません。

---

### 設計フェーズ

設計では、具体的なコードより先に境界を決めます。

最低限の設計項目はこれです。

```text
目的
入力
出力
責務
非責務
不変条件
失敗時の振る舞い
信頼境界
依存先
下流への契約
```

設計文書のテンプレートは、かなり単純で構いません。

```markdown
# Design: <名前>

## Problem

## Goal

## Non-goals

## Inputs

## Outputs

## Invariants

## Boundaries

## Failure behavior

## Proposed components

## Dependency order

## Acceptance criteria

## Open questions
```

この段階では、「どう実装するか」より、

> 何を保証するか
> 何を保証しないか

を固定します。

---

### 人間による設計ゲート

ここが最重要です。

LLMが設計したら、そのまま実装へ進ませません。

人間は最低限、次を確認します。

```text
問題を取り違えていないか
責務が広すぎないか
既存契約を再定義していないか
余計なpluginや抽象化を増やしていないか
信頼境界が壊れていないか
失敗を黙って握りつぶしていないか
テスト可能な形になっているか
```

この時点で設計を承認する言葉も明示します。

```text
この設計で進めてよい
```

または、

```text
以下を修正して再提出
```

曖昧な「よさそう」より、ゲート通過を明確にした方がLLMは暴走しにくいです。

---

### タスク分解

設計承認後に、実装タスクへ分解します。

良いタスクは、なるべく次を満たします。

```text
単一の責務
明確な入力と出力
単独でテスト可能
変更範囲が限定される
完了条件が機械的に確認可能
他タスクとの依存が明示される
```

これを「pure独立タスク」と呼んでよいと思います。

厳密には完全なpure functionでなくても、

> 他タスクの未確定実装に依存せず、閉じた契約の中で完了判定できる

なら独立タスクです。

---

#### タスク定義テンプレート

```markdown
# Task: <名前>

## Parent design

## Goal

## Depends on

## Owned paths

## Inputs

## Outputs

## Required behavior

## Required red tests

## Boundaries

## Forbidden changes

## Done when

## Report format
```

特に重要なのは、次の4項目です。

##### Owned paths

触ってよい範囲。

```text
packages/schema-normalization/src/parser/**
packages/schema-normalization/src/parser.test.ts
```

##### Required red tests

最初に失敗させるテスト。

```text
JSON文字列内の中括弧でrecord boundaryが壊れない
```

##### Forbidden changes

勝手に広げてはいけない範囲。

```text
Offering schemaを変更しない
Proxy hot pathを変更しない
network fetchを追加しない
```

##### Done when

完了条件。

```text
focused tests green
package typecheck green
package build green
root tests green
diff-check green
```

---

### 人間による分解ゲート

タスクをLLMへ投げる前に、もう一度人間が確認します。

確認項目は以下です。

```text
タスク同士の責務が重複していないか
同じファイルを複数タスクが触らないか
設計判断が各実装者へ漏れていないか
依存順が正しいか
個別に赤→緑できるか
統合順が明確か
```

この段階で、依存グラフを書いておくと非常に強いです。

```text
A 契約
├─ B parser
├─ C fingerprint
└─ D adapter
       ↓
     E pipeline
       ↓
     F integration
```

---

### 実装プロンプト化

承認済みタスクを、そのままLLMへコピペできる形式（つまり、サブエージェントに投げられる形）にします。

重要なのは、LLMに「考えて全部やって」ではなく、

> この閉じたタスクだけを実行し、証拠を返せ

と指示することです。

---

#### 汎用実装プロンプト

```text
以下のタスクだけを実装してください。

タスク:
<タスク本文>

作業規則:
- 最初に対象コードと既存テストを確認する
- 必須の失敗テストを先に追加する
- 失敗を確認してから最小実装を行う
- owned paths外を変更しない
- forbidden changesを行わない
- 公開契約を勝手に増やさない
- 横断ドキュメントや完了状態を変更しない
- コミット済みを完了扱いしない

完了時に報告すること:
- 変更ファイル一覧
- 追加したテスト
- Redの証拠
- Greenの証拠
- typecheck/build結果
- 残る未達条件
- HEAD SHA
- 作業ツリー状態
```

---

### 実装フェーズ

実装LLMは次の順序で動きます。

```text
既存状態確認
  ↓
赤テスト追加
  ↓
Red確認
  ↓
最小実装
  ↓
focused test
  ↓
package test
  ↓
root test
  ↓
typecheck/build
  ↓
diff-check
  ↓
報告
```

ここで重要なのは、

> コードを書くことより、完了証拠を作ること

です。

---

### 統合前レビュー

実装報告を受けても、すぐ統合しません。

確認するのは、

```text
契約どおりか
境界を越えていないか
テストが実経路を証明しているか
helperだけをテストしていないか
失敗ケースを握り潰していないか
未確認事項を成功扱いしていないか
```

レビュー結果は明確に分けます。

```text
blocking findingあり
blocking findingなし
```

「なんとなくよい」は使わない方がよいです。

---

### 統合フェーズ

統合時には、実装そのものだけでなく、統合後状態を検証します。

```text
最新mainを取り込む
競合を解消する
全体test
typecheck
build
CI
差分確認
mainへ統合
```

重要なのは、

> feature branchでgreen
> と
> main統合後にgreen

は別物だということです。

---

### 完了判定

完了条件は以下がすべて揃ったときです。

```text
実装がmainへ統合済み
統合後test green
統合後CI green
設計上のacceptance criteriaを満たす
blocking findingなし
関連Issueをclose可能
正本docsを同期済み
```

つまり、

```text
commit != done
branch green != done
local test green != CI green
```

です。

---

### 正本ドキュメント同期

最後に、設計と実装状態を同期します。

更新対象は通常、

```text
ROADMAP
IMPLEMENTATION_STATUS
task ledger
Issue state
design change history
```

です。

実装担当LLMにはこれをやらせず、統合時にまとめて行う方が安全です。

---

## 統括LLMがいる場合

役割はこう分けます。

```text
人間
  ├─ 問題と優先順位を決める
  ├─ 設計を承認する
  ├─ タスク分解を承認する
  └─ 最終統合を承認する

統括LLM
  ├─ 設計案を作る
  ├─ タスクへ分解する
  ├─ 依存関係を管理する
  ├─ 実装報告を監査する
  └─ Docs Syncする

実装LLM
  ├─ 指定タスクだけ実装
  ├─ TDD
  └─ 証拠を報告
```

---

## 統括LLMがいない場合

IDE一本、単一セッションでも同じ流れを再現できます。

この場合は、**セッション内で役割を明示的に切り替える**のが重要です。

---

### 単一セッションのモード

```text
MODE 1: DESIGN
MODE 2: DECOMPOSE
MODE 3: IMPLEMENT
MODE 4: REVIEW
MODE 5: INTEGRATE
MODE 6: DOCS SYNC
```

LLMへ毎回モードを宣言します。

---

#### DESIGNモード

```text
現在は設計モードです。

コードは変更しないでください。
問題、境界、契約、不変条件、非目標、
依存関係、acceptance criteriaだけを整理してください。

実装案は候補として示してよいですが、
実装作業は開始しないでください。
```

---

#### DECOMPOSEモード

```text
現在はタスク分解モードです。

承認済み設計を、
独立してテスト可能なタスクへ分解してください。

各タスクに以下を含めてください:
- depends_on
- owned_paths
- required red test
- forbidden changes
- done_when

コードは変更しないでください。
```

---

#### IMPLEMENTモード

```text
現在は実装モードです。

対象タスクは一つだけです。
設計変更や横展開は行わないでください。
最初に失敗テストを追加し、Redを確認後、
最小実装でGreenにしてください。
```

---

#### REVIEWモード

```text
現在はレビュー専用モードです。

コードを変更しないでください。
タスク契約、diff、テスト、実行経路、
失敗時挙動、owned_paths違反を監査してください。

blocking findingだけを列挙し、
なければ「blocking findingなし」と明記してください。
```

---

#### INTEGRATEモード

```text
現在は統合モードです。

最新mainを取り込み、
競合を最小限に解消し、
全体test/typecheck/buildを実行してください。

新機能や追加リファクタは行わないでください。
```

---

#### DOCS SYNCモード

```text
現在はDocs Syncモードです。

実装コードは変更しないでください。
監査済みの事実だけを、
ROADMAP、実装状況、台帳、Issue状態へ反映してください。

未確認事項をdoneにしないでください。
```

---

### 単一セッションでの最大の注意点

同じLLMセッションでは、設計者・実装者・レビュアーが同一なので、自己肯定バイアスが起きます。

そのため、レビュー時に意図的にコンテキストを切ります。

```text
先ほどの実装意図は信用せず、
現在のdiffとテストだけを証拠として監査してください。
```

さらに、

```text
実装者の説明ではなく、
コードと実行結果を正本として扱ってください。
```

と指示します。

可能ならレビュー前にコミットし、

```text
git diff <base>..<head>
```

だけを見せると、かなり独立性が上がります。

---

## 汎用的な最小ワークフロー

一番短くするとこうです。

```text
1. 問題を書く
2. 設計を書く
3. 人間が設計承認
4. 独立タスクへ分解
5. 人間が分解承認
6. 1タスクずつTDD実装
7. diffとテストをレビュー
8. 最新mainへ統合
9. 統合後CI
10. docsとIssueを同期
```

---

## この方式の原則

最後に、原則だけ抜き出すと以下です。

```text
設計判断は直列
契約済み実装は並列

一つのタスクは一つの責務
一つのタスクは独立して赤→緑

実装者は完了を宣言しない
コミット済みは完了ではない

説明より証拠
helper testより実経路
local greenより統合後CI

未確定契約を下流へ漏らさない
unknownを勝手にknownへ変えない

横断状態更新は最後に一度だけ
```

これを一文で言うなら、

> 人間が設計と分解を承認し、LLMには境界の閉じたタスクだけをTDDで実装させ、証拠を監査してから統合する。

です。
