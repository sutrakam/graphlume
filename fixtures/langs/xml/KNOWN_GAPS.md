# XML: no definition query is registered

XML declares no construct common to the language. Every vocabulary built on it
(Maven POM, Spring beans, Android layouts, .csproj, SVG, plist, DocBook) spells
its names differently, and the generic ones (`name`, `version`, `class`, `ref`)
are values of the data, repeated in every file. The one candidate was an
element with an `id` or `xml:id` attribute. It was tried (a query on
`STag` / `EmptyElemTag` attributes named `id` or `xml:id`, value without its
quotes) and rejected on real files, so no query is registered and there is no
`expected.tsv`.

## Evidence

The query was run over the XML samples of the linguist corpus. In the seven
files of the partial run that carry an id, it produced 71 symbols:

| file | ids | what they are |
| --- | --- | --- |
| `water.tsx` | 30 | the numbers 0, 1, 2, 3, ...  |
| `CSharpVSPackage.vstemplate` | 19 | assembly names (`Microsoft.VisualStudio.Shell.Interop.8.0`) |
| `main.ncl`, `receiver.ncl` | 6, 5 | meaningful (`luaRegion`, `programSettings`) |
| `Application.xib` | 5 | Xcode-generated (`h4M-LL-qlT`, `-1`, `-2`) |
| `battlescribe.gst` | 5 | UUIDs (`f7896daf-884b-4236-8045-96e64cb5d92c`) |
| `Strings.resx` | 1 | |

Counting ids that are a UUID, a bare number, an Xcode object id or end in six
or more digits, 40 of 71 are tool-generated. The partial run was stopped before
the corpus finished; it also reached an Eclipse CDT `.cproject` in the first
pass, with 143 ids in the one file (`cdt.managedbuild.config.gnu.exe.debug.1803931088`,
and the same id twice, once as a definition and once as a reference).

A developer does not look these up by name, and a repository with an Eclipse,
Xcode or Qt project would gain hundreds of them. A filter for generated ids
(UUID, numeric suffix, Xcode pattern) is a list of tools, not a rule, and it
would still keep ids that are references repeated beside the declaration.

## What the tree has

```
<bean id="dataSource" class="com.example.DataSource">
  (element (STag (Name "bean") (Attribute (Name "id") (AttValue "\"dataSource\"")) ...))
<section xml:id="intro">
  (STag (Name "section") (Attribute (Name "xml:id") (AttValue ...)))
<item id=""/>
  (EmptyElemTag (Name) (Attribute (Name) (AttValue "\"\"")))
```

`AttValue` includes the quotes, so a name fix is needed to strip them; the
attribute name `xml:id` is a single `Name`.

## Where XML is already covered

`pom.xml` is read as a dependency manifest by `internal/analyze/deps`
(`ecosystemOf`). SVG files are not claimed by this grammar (the walker records
them as `no grammar for this file type`).

If a vocabulary is worth indexing (Spring beans, Android `android:id`,
DocBook), the right place is a rule that knows the vocabulary, not the grammar.
