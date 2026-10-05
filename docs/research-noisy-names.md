# Noisy ebook file names: research corpus

ember's name cleaner (`internal/names`) proposes `Title - Author.ext` for a Book. This page records the
real file names it was built and tested against, where each was seen, and the name it proposes from the
file name alone (no metadata). Every row is a table test: the four user files in
`TestCleanUsersOwnFiles`, the rest in `TestCleanCorpus` (`internal/names/corpus_test.go`).

**116 names**: 4 from the user's own Library and 112 seen verbatim on the web, in English, Portuguese,
Spanish, Japanese, Chinese, Polish, Greek and French.

## How the corpus was collected

Names were copied character for character from pages that could be fetched in full: GitHub issue text
(crash logs, KOReader "opening file" lines, attachment names), GitHub repository file paths, and the
archive.org metadata API, which lists files under their original upload names. Nothing was reconstructed.

- Reddit (r/ebooks, r/kindle, r/Calibre, r/Annas_Archive) blocks both search and direct fetches from the
  research tooling, so it yielded nothing.
- MobileRead only surfaced generic threads about renaming, with no verbatim names.
- Search-engine snippets returned no usable file names.
- Two sources HTML-escaped `&` as `&amp;` and one shell-escaped the brackets (`\( PDFDrive.com \)`).
  The corpus holds the names as they are on disk: `&` and plain brackets.

Seen, but left out of the tests because they are not Books ember would clean:

- **Zip wrappers**, whose `.zip` is not a Book format: `... (z-library.sk, 1lib.sk, z-lib.sk).epub.zip`
  ([bookfere/Ebook-Translator-Calibre-Plugin#587](https://github.com/bookfere/Ebook-Translator-Calibre-Plugin/issues/587)),
  `Der Graf von Monte Christo (komplette Ausgabe) -- ... -- Anna’s Archive.epub.zip`
  ([uxjulia/CrossInk#740](https://github.com/uxjulia/CrossInk/issues/740)), `Tokyo Noir ... (z-library.sk, 1lib.sk, z-lib.sk).zip`
  ([archive.org](https://archive.org/download/tokyo-noir-in-and-out-of-japans-underworld-jake-adelstein-z-library.sk-1lib.sk-z-lib.sk/)),
  `Blood in the Machine The Origins of t... (Z-Library).epub.zip`
  ([immersive-translate#1275](https://github.com/immersive-translate/immersive-translate/issues/1275)).
- **Names without an extension**: scene release folders such as `Ellen.Hopkins.Impulse.2008.RETAiL.EPUB.eBook-NODE`
  ([Bibliophilarr#72](https://github.com/Swartdraak/Bibliophilarr/issues/72)), `Software Architecture An Engineering Approach (TruePDF) (Mark Richards, Neal Ford) (z-library.sk, 1lib.sk, z-lib.sk)`
  ([dsh-web-studyhub#201](https://github.com/EricWang1358/dsh-web-studyhub/issues/201)), and an md5 name a downloader
  bug left without one ([shelfmark#214](https://github.com/calibrain/shelfmark/issues/214)).

## Naming conventions per site

- **Anna's Archive**: `Title -- Author(s) -- Edition/Series, Year -- Publisher -- ISBN -- md5 -- Anna’s Archive.ext`.
  - Empty fields are left out or written `None`, and the ISBN is sometimes labelled `isbn13`.
  - `: / ? .` inside fields become `_` (`Title_ Subtitle`, `S_ C_`, `1_ edition`).
  - Long names are cut short, even inside the last field (`... -- Ann.pdf`).
  - The apostrophe is usually U+2019.
  - After an upload elsewhere, spaces can become dots (`.--.Anna.s.Archive`).
  - There is also a short form, `annas-arch-<hex>.epub`.
  - See [alfred-quick-file-access#3](https://github.com/chrisgrieser/alfred-quick-file-access/issues/3) and
    [shelfarr#541](https://github.com/Pedro-Revez-Silva/shelfarr/issues/541).
- **Z-Library**: the current form is `Title (Author) (z-library.sk, 1lib.sk, z-lib.sk).ext`.
  - Older forms are `Title (Author) (Z-Library).ext` and `Title by Author (z-lib.org).ext`.
  - Titles are cut with `...`, and colons are dropped.
  - Other tools mangle the tag (`_z_library_sk,_1lib_sk,`, `-_z-library.sk_-1lib.sk_-z-lib.sk_`).
- **Library Genesis**: `Author - Title (Year, Publisher) - libgen.li.ext` (or `.lc`).
  - Variants have an optional `[Series]` prefix, `Title.libgen.li.ext` (from a calibre plugin), and
    `Title (Series){Author}(Year, Publisher){ID} libgen.li.ext`.
- **OceanofPDF**: `_OceanofPDF.com_Title_-_Author.ext`, with underscores for spaces. Case is kept as uploaded,
  sometimes all lower case ([archive.org/download/oceanofpdf](https://archive.org/download/oceanofpdf/)).
- **PDFDrive**: `Title ( PDFDrive ).pdf` or `Title ( PDFDrive.com ).pdf`; `:` becomes `_`.
- **Kindle / DeDRM**: `Title (Spanish Edition).azw3`; for KFX, `Title (Portuguese Edition)_<ASIN>.kfx`.
- **ePubLibre**: `Surname, Name - [Series NN] Title (r1.0).epub`.
  - The series can come first, and two authors are joined with `&`.
  - Facsimiles use `Surname, Name - Title [EPL-FS] [year] [year].pdf`.
- **Standard Ebooks**: `author-slug_title-slug_extra.epub`, all lower case.
- **Calibre**: `Title - Author.ext`, with `_` for characters the file system refuses and a `(N)` book id on clashes.

## Rules (from the ticket) and the judgment calls they forced

The ticket settles the format, the order of sources, the author rules and what to drop. Where a corpus name
needed a call the ticket does not make, the call is listed here and encoded as that row's expectation.

1. **A name that already reads clean is not touched, not even with metadata.** If neither a site pattern
   nor the generic cleanup changes anything (`Dune.epub`, `Dune - Frank Herbert.epub`), there is nothing
   better to propose, and a name someone chose by hand stays.
2. **Bare `A - B` keeps its order.** Without a site pattern, ember cannot tell `Title - Author` (Calibre)
   from `Author - Title` (libgen, many users), so each part is cleaned in place and none is swapped
   (rows 93, 103, 105, 111).
3. **Series in brackets stay.** The ticket lists year, edition, publisher, ISBN, hash and source tags; a
   series is none of these. `(Asesino de Brujas, 3)`, `[Sam Wyndham 001]` and `(FGV Management)` are kept.
   Imprints in brackets (`(Perennial Classics)`, `(Delphi Classics)`) count as publisher and go.
4. **A colon always cuts a subtitle; a dash only when what follows is plainly one** (`- A Novel`). This
   holds for Anna's Archive, libgen and PDFDrive `_` standing for `:`, and for the full-width `：`. A name
   whose site already dropped the colon keeps its subtitle, since nothing marks where it begins
   (rows 27, 49, 50). An edition cuts the title where it starts (`Python Crash Course, 2nd Edition A
   Hands-On ...` gives `Python Crash Course`).
5. **`Last, First` is turned round only when it is clearly one person.**
   - It is turned when the surname side has one word (`Orwell, George`), when the given side has one word
     or only initials (`García Márquez, Gabriel`, `Gwynne, S. C.`), or for `Le Guin, Ursula K.`.
   - `Rachel Botsman, Roo Rogers` stays two people.
   - East Asian names are not turned: `鎌池, 和馬` gives `鎌池 和馬`.
   - A surname written first with no comma cannot be detected and stays (`Sanderson Brandon`, row 53).
6. **Roles.** Helpers are dropped with their names: translator, illustrator, foreword, and "drawings by …".
   An editor or organizer keeps the name and loses the note (`Jane Doe (editor)` gives `Jane Doe`).
   `author` and `None` placeholders are dropped, and so are credentials (`MD`, `Ph.D.`).
7. **3+ authors**: `First et al.`. The extension follows directly, so the name ends `et al.epub`, not `et al..epub`.
8. **What Z-Library puts in the author slot is taken as given**, even an uploader's handle (`BLReads`,
   row 36): the convention says that is the author, and ember does not guess otherwise.
9. **Unrecoverable names get no change rather than a guess**: `annas-arch-<hex>`, `isbn_<13>_lcp`, and a
   libgen magazine made only of dates (rows 18, 68, 112). Cut-off titles stay cut (`Red notice a tru`,
   row 60).
10. **Case**: titles and authors from a file name are fixed only when all upper or all lower case, part by
    part (`ANIMAL FARM - George Orwell` gives `Animal Farm - George Orwell`). Roman numerals stay upper. Short
    joining words in English, Portuguese and Spanish stay lower. Metadata keeps its case.
11. **Characters**: accents and non-Latin scripts are kept. `\ / | :` become dashes. `* ? < >` go. Straight
    and curly double quotes, and the `''` Anna's Archive writes for `"`, become `'`. `© ® ™` go.
12. **Leftovers that only lose a trailing dot** still count as a change (row 110). This is harmless, and
    the only way to avoid it would be a special case.

Upload uses the file-name rules only: the Book is not on the Device yet when its name is checked, and
reading its metadata after the part is written would mean a second pass over the network before the
rename. The preview reads metadata, so a Book uploaded under a pattern name can be cleaned again later.

## Corpus

"No change" means the cleaner proposes nothing for that name.

| # | file name as found | proposed (from the name alone) | lang | family | source |
|---|---|---|---|---|---|
| U1 | `Animal Farm (AmazonClassics Edition) -- Orwell, George -- AmazonClassics Edition, 2021 -- AmazonClassics -- 9eb17e015e45ed6e371a245ee96e84ab -- Anna's Archive.epub` | `Animal Farm - George Orwell.epub` | en | Anna's Archive | the user's own Library |
| U2 | `Do zero ao infinito (e além) -- Mike Goldsmith -- 2016 -- Benvirá -- isbn13 9788557170612 -- f5c597940cfefa073fb07b2d5b8d0c12 -- Anna's Archive.epub` | `Do zero ao infinito (e além) - Mike Goldsmith.epub` | pt | Anna's Archive | the user's own Library |
| U3 | `The Little Schemer, fourth edition -- Daniel P_ Friedman, Matthias Felleisen; drawings by Duane -- Schemer (1), 4 edition, December 21, 1995 -- The -- isbn13 9780262560993 -- e851170d78a83eaf5596616ce3321889 -- Ann.pdf` | `The Little Schemer - Daniel P. Friedman & Matthias Felleisen.pdf` | en | Anna's Archive | the user's own Library |
| U4 | `O Velho E O Mar -- Ernest Hemingway -- 2013;2016 -- Edição ''Livros do Brasil'' -- e8b179d8f63db8ad52a69e10015f7367 -- Anna's Archive.mobi` | `O Velho E O Mar - Ernest Hemingway.mobi` | pt | Anna's Archive | the user's own Library |
| 1 | `Marx in the Anthropocene -- Kohei Saito -- First published, Cambridge, 2022 -- Cambridge University Press -- 9781009366182 -- 139cd518699a31475eb67baa3da70bc5 -- Anna’s Archive.pdf` | `Marx in the Anthropocene - Kohei Saito.pdf` | en | Anna's Archive | https://github.com/chrisgrieser/alfred-quick-file-access/issues/3 |
| 2 | `What's mine is yours_ The rise of collaborative consumption -- Rachel Botsman, Roo Rogers -- 1_ edition, 2010 -- HarperCollins;Harper Business -- 9780007395910 -- f95adce3598ecf3c10b321f180241da2 -- Anna’s Archive.epub` | `What's mine is yours - Rachel Botsman & Roo Rogers.epub` | en | Anna's Archive | https://github.com/chrisgrieser/alfred-quick-file-access/issues/3 |
| 3 | `An Echo in the Bone_ A Novel (Outlander, Book 7) -- Gabaldon, Diana -- Frasers 7, 2009 -- Delacorte Press -- 9780385342452 -- 56045d8899fa4aa908ed413e6a9f7f40 -- Anna’s Archive.epub` | `An Echo in the Bone - Diana Gabaldon.epub` | en | Anna's Archive | https://github.com/crocodilestick/Calibre-Web-Automated/issues/356 |
| 4 | `Unaccustomed Earth -- Lahiri, Jhumpa -- c4417dd09ccf55a5c17d67db519eefee -- Anna’s Archive.epub` | `Unaccustomed Earth - Jhumpa Lahiri.epub` | en | Anna's Archive (short) | https://github.com/daniel-j/send2ereader/issues/42 |
| 5 | `Programming with Python for Engineers -- Sinan Kalkan; Onur T_ Şehitoğlu; Göktürk Üçoluk -- 2, 2025 -- Springer Nature Switzerland AG -- 9783031571473 -- 298abeeaa8487bf60f3872b2574174f2 -- Anna’s Archive.pdf` | `Programming with Python for Engineers - Sinan Kalkan et al.pdf` | en | Anna's Archive | https://github.com/anthropics/claude-code/issues/725 |
| 6 | `Pictures and Tears _ A History of People Who Have Cried in -- James Elkins -- Taylor & Francis (Unlimited), New York, 2004 -- Routledge -- isbn13 9780203990322 -- e4499a214f87a179cfad644bc88ee76b -- Anna’s Archive.pdf` | `Pictures and Tears - James Elkins.pdf` | en | Anna's Archive (isbn13, cut short) | https://github.com/Eunbi-Ko-0223/Book-Summary-Pictures-and-Tears/issues/1 |
| 7 | `Pictures.and.Tears._.A.History.of.People.Who.Have.Cried.in.--.James.Elkins.--.Taylor.Francis.Unlimited.New.York.2004.--.Routledge.--.isbn13.9780203990322.--.e4499a214f87a179cfad644bc88ee76b.--.Anna.s.Archive.pdf` | `Pictures and Tears - James Elkins.pdf` | en | Anna's Archive, dotted by an upload | https://github.com/Eunbi-Ko-0223/Book-Summary-Pictures-and-Tears/issues/1 |
| 8 | `_On.the.Republic_.and._On.the.Laws_.--.Marcus.Tullius.Cicero.David.Fott.--.Agora.Editions.2019.--.Cornell.University.Press.--.isbn13.9780801452239.--.14c2ef5a6a3371b3d5eb9fead4f56560.--.Anna.s.Archive.pdf` | `On the Republic - Marcus Tullius Cicero David Fott.pdf` | en | Anna's Archive, dotted | https://github.com/hydropix/TranslateBooksWithLLMs/issues/149 |
| 9 | `Invitation to a Banquet _ The Story of Chinese Food -- Fuchsia Dunlop -- S_l, 2023 -- Penguin Random House UK -- isbn13 9780241516980 -- 03a727caf05d1f17998cf5afb36449a5 -- Anna's Archive.epub` | `Invitation to a Banquet - Fuchsia Dunlop.epub` | en | Anna's Archive | https://github.com/inkpot-monkey/palimpsest/issues/144 |
| 10 | `Watership Down (Perennial Classics) -- Adams, Richard -- Watership Down 1, 2001 -- Harper Perennial Modern Classics -- 9780027000306 -- 95313f87879258967b667db6da8fa1bd -- Anna’s Archive.epub` | `Watership Down - Richard Adams.epub` | en | Anna's Archive (series field) | https://github.com/merrilin-ai/user-issues/issues/6 |
| 11 | `Empire of the Summer Moon -- Gwynne, S_ C_ -- 2009 -- ePubLibre -- c25e91746f94864481b315eeae7a439d -- Anna’s Archive.epub` | `Empire of the Summer Moon - S. C. Gwynne.epub` | en | Anna's Archive (ePubLibre as publisher) | https://github.com/gabrielmeleiro1/openmlupload-testing/issues/105 |
| 12 | `Psychologia_ Kluczowe koncepcje Tom 4 -- Philip Zimbardo, Robert L_ Johnson, Vivian McCann -- 2018 -- Wydawnictwo Naukowe PWN -- 44eb8f090bfc77926fdd92d7edf834ab -- Anna’s Archive.epub` | `Psychologia - Philip Zimbardo et al.epub` | pl | Anna's Archive | https://github.com/koreader/koreader/issues/15466 |
| 13 | `また、同じ夢を見ていた (双葉文庫) -- 住野よる, author -- Futaba bunko, Tōkyō, 2018 -- 株式会社 双葉社 -- 9784575521252 -- 8e45ad853a7930a486caa4e6352633c4 -- Anna’s Archive.epub` | `また、同じ夢を見ていた (双葉文庫) - 住野よる.epub` | ja | Anna's Archive | https://github.com/koreader/koreader/issues/14541 |
| 14 | `征服少女_AXIS girls_ -- 古野まほろ -- None -- 光文社 -- 0c28af397ec57159abb15cb65f07853b -- Anna’s Archive.epub` | `征服少女 AXIS girls - 古野まほろ.epub` | ja | Anna's Archive (literal None) | https://github.com/auto-novel/auto-novel/issues/332 |
| 15 | `新約 とある魔術の禁書目録(8) -- 鎌池, 和馬 -- Dengeki bunko, Tōkyō, 2013 -- 株式会社KADOKAWA -- isbn13 9784048919043 -- 28f8a1c91f2961382a7a3742218698ce -- Anna’s Archive.epub` | `新約 とある魔術の禁書目録(8) - 鎌池 和馬.epub` | ja | Anna's Archive | https://github.com/zeeyado/koassistant.koplugin/issues/111 |
| 16 | `Twilight © -- Stephenie Meyer -- ... -- Anna’s Archive.epub` | `Twilight - Stephenie Meyer.epub` | en | Anna's Archive (fields elided) | https://github.com/Pedro-Revez-Silva/shelfarr/issues/541 |
| 17 | `思考，快与慢…Anna's Archive.pdf` | `思考，快与慢.pdf` | zh | Anna's Archive (ellipsis) | https://github.com/daniel4828/biangbiangmian3000/issues/673 |
| 18 | `annas-arch-86b7e6051d66.epub` | (no change) | ? | Anna's Archive short hash | https://github.com/koreader/koreader/issues/13870 |
| 19 | `Os Sofrimentos do Jovem Werther (Portuguese Edition) -- Johann Wolfgang Goethe -- 2001 -- L&PM Editores -- d82ce0fd85efc2e69d6a8c7fe91fc2ec -- Anna’s Archive.pdf` | `Os Sofrimentos do Jovem Werther - Johann Wolfgang Goethe.pdf` | pt | Anna's Archive + Kindle edition | https://archive.org/download/os-sofrimentos-do-jovem-werther-portuguese-edition-johann-wolfgang-goethe-2001-l/ |
| 20 | `O Santo e a Porca -- Ariano Suassuna -- 2014 -- LeLivros -- e0306c5bf22cc942c29b2d48e6990357 -- Anna’s Archive.pdf` | `O Santo e a Porca - Ariano Suassuna.pdf` | pt | Anna's Archive (LeLivros) | https://archive.org/download/mark-twain-um-assassinato-um-misterio-e-um-casamento/ |
| 21 | `Homens e Caranguejos -- Josué de Castro -- 2024 -- a3afdc2e900667956cd5474701328d08 -- Anna’s Archive.epub` | `Homens e Caranguejos - Josué de Castro.epub` | pt | Anna's Archive | https://archive.org/download/mark-twain-um-assassinato-um-misterio-e-um-casamento/ |
| 22 | `Cabala Umbandista -- Tancredo da Silva Pinto -- 1, 1, 1971 -- Editora Espiritualista -- 07ccebe722acf1cc96bd878696d99166 -- Anna’s Archive (1).pdf` | `Cabala Umbandista - Tancredo da Silva Pinto.pdf` | pt | Anna's Archive + (1) | https://archive.org/download/cabala-umbandista-tancredo-da-silva-pinto-1-1-1971-editora-espiritualista-07cceb/ |
| 23 | `Complexo, arquétipo e símbolo na psicologia de Carl Gustav -- Jolande Jacobi -- 1, 2016 -- Editora Vozes Limitada -- 9788532653284 -- bcb1ac620b4e5306c4ec158206d0fe37 -- Anna’s Archive.pdf` | `Complexo, arquétipo e símbolo na psicologia de Carl Gustav - Jolande Jacobi.pdf` | pt | Anna's Archive (cut short) | https://archive.org/download/complexo-arquetipo-e-simbolo-na-psicologia-de-carl-gustav-jolande-jacobi-1-2016-/ |
| 24 | `A parte maldita -- Georges Bataille -- 2014 -- Autêntica Editora -- 9788582170472 -- f54d17b23342ffc91091aa2290cf5cea -- Anna’s Archive.pdf` | `A parte maldita - Georges Bataille.pdf` | pt | Anna's Archive | https://archive.org/download/a-parte-maldita-georges-bataille-2014-autentica-editora-9788582170472-f-54d-17b-/ |
| 25 | `O Escravo na Jurisprudência Brasileira_ Magistratura & -- Lenine Nequete -- 1a, 1988 -- Diretoria da Revista de Jurisprudência e Outros Impressos do -- 8123acacc77d432ca940abc50444c36f -- Anna’s Archive.pdf` | `O Escravo na Jurisprudência Brasileira - Lenine Nequete.pdf` | pt | Anna's Archive (cut short) | https://archive.org/download/o-escravo-na-jurisprudencia-brasileira-magistratura-lenine-nequete-1a-1988-diret/ |
| 26 | `Tupã Tenondé_ a Criação do Universo, da Terra e do Homem -- Kaka Werá Jecupé -- 2001 -- Fundação Peirópolis -- 19755967f2764baa41cea4d65408d9a9 -- Anna’s Archive.pdf` | `Tupã Tenondé - Kaka Werá Jecupé.pdf` | pt | Anna's Archive | https://archive.org/download/tupa-tenonde-a-criacao-do-universo-da-terra-e-do-homem-kaka-wera-jecupe-2001-fun/ |
| 27 | `Genesis The Scientific Quest for Lifes Origins (Robert Hazen) (z-library.sk, 1lib.sk, z-lib.sk).pdf` | `Genesis The Scientific Quest for Lifes Origins - Robert Hazen.pdf` | en | Z-Library .sk | https://github.com/crafteve/CellAbo/issues/1 |
| 28 | `O-caderno-de-maldades-do-Scorpio-2-_Maidy-Lacerda_-_z-library.sk_-1lib.sk_-z-lib.sk_.pdf` | `O caderno de maldades do Scorpio 2 Maidy-Lacerda.pdf` | pt | Z-Library .sk, mangled | https://archive.org/download/o-caderno-de-maldades-do-scorpio-2-maidy-lacerda-z-library.sk-1lib.sk-z-lib.sk/ |
| 29 | `Direito Tributário OAB 1ª e 2ª Fases (Josiane Minardi) (z-library.sk, 1lib.sk, z-lib.sk).pdf` | `Direito Tributário OAB 1ª e 2ª Fases - Josiane Minardi.pdf` | pt | Z-Library .sk | https://github.com/almeidagomes94-art/felipe-/issues/1 |
| 30 | `Dioses y Monstruos (Asesino de Brujas, 3) (Shelby Mahurin) (z-library.sk, 1lib.sk, z-lib.sk).pdf` | `Dioses y Monstruos (Asesino de Brujas, 3) - Shelby Mahurin.pdf` | es | Z-Library .sk | https://archive.org/download/dioses-y-monstruos-asesino-de-brujas-3-shelby-mahurin-z-library.sk-1lib.sk-z-lib.sk/ |
| 31 | `Asesino de brujas 1 (la bruja blanca) (Shelby Mahurin) (z-library.sk, 1lib.sk, z-lib.sk).pdf` | `Asesino de brujas 1 (la bruja blanca) - Shelby Mahurin.pdf` | es | Z-Library .sk | https://archive.org/download/dioses-y-monstruos-asesino-de-brujas-3-shelby-mahurin-z-library.sk-1lib.sk-z-lib.sk/ |
| 32 | `Los hijos del rey (Shelby Mahurin) (z-library.sk, 1lib.sk, z-lib.sk).pdf` | `Los hijos del rey - Shelby Mahurin.pdf` | es | Z-Library .sk | https://archive.org/download/dioses-y-monstruos-asesino-de-brujas-3-shelby-mahurin-z-library.sk-1lib.sk-z-lib.sk/ |
| 33 | `1984 (George Orwell) (z-library.sk, 1lib.sk, z-lib.sk).pdf` | `1984 - George Orwell.pdf` | ? | Z-Library .sk | https://archive.org/download/dioses-y-monstruos-asesino-de-brujas-3-shelby-mahurin-z-library.sk-1lib.sk-z-lib.sk/ |
| 34 | `Vogue on  Alexander McQueen (Chloe Fox) (z-library.sk, 1lib.sk, z-lib.sk).epub` | `Vogue on Alexander McQueen - Chloe Fox.epub` | en | Z-Library .sk | https://archive.org/download/vogue-on-alexander-mc-queen-chloe-fox-z-library.sk-1lib.sk-z-lib.sk/ |
| 35 | `Clinical Pediatric Neurology A Signs and Symptoms Approach, 6th Edition (Gerald Fenichel MD) (z-library.sk, 1lib.sk, z-lib.sk).pdf` | `Clinical Pediatric Neurology A Signs and Symptoms Approach - Gerald Fenichel.pdf` | en | Z-Library .sk + edition | https://archive.org/download/clinical-pediatric-neurology-a-signs-and-symptoms-approach-6th-edition-gerald-fe/ |
| 36 | `I Really Can’t Drive a Mecha (BLReads) (z-library.sk, 1lib.sk, z-lib.sk).EPUB` | `I Really Can't Drive a Mecha - BLReads.epub` | en | Z-Library .sk, upper-case extension | https://archive.org/download/i-really-cant-drive-a-mecha-blreads-z-library.sk-1lib.sk-z-lib.sk/ |
| 37 | `The Red Panda Raises Cubs and Farms in the Beast World (Hui Jian Ru Yu 灰剑如羽) (z-library.sk, 1lib.sk, z-lib.sk).EPUB` | `The Red Panda Raises Cubs and Farms in the Beast World - Hui Jian Ru Yu 灰剑如羽.epub` | en/zh | Z-Library .sk | https://archive.org/download/i-really-cant-drive-a-mecha-blreads-z-library.sk-1lib.sk-z-lib.sk/ |
| 38 | `He_Who_Fights_With_Monsters_12_A_Li_z_library_sk,_1lib_sk,.epub` | `He Who Fights With Monsters 12 A Li.epub` | en | Z-Library, underscored and cut | https://github.com/koreader/koreader/issues/16020 |
| 39 | `Heretical Fishing 2 A Cozy Guide to Annoying the Cults, Outsmarting the Fish, and Alienating Oneself Haylock Jobson z-library.sk, 1lib.sk, z-lib.sk.epub` | `Heretical Fishing 2 A Cozy Guide to Annoying the Cults, Outsmarting the Fish, and Alienating Oneself Haylock Jobson.epub` | en | Z-Library, brackets stripped | https://github.com/koreader/koreader/issues/16020 |
| 40 | `一億年のテレスコープ (春暮 康一) (z-library.sk, 1lib.sk, z-lib.sk).epub` | `一億年のテレスコープ - 春暮 康一.epub` | ja | Z-Library .sk | https://github.com/auto-novel/auto-novel/issues/332 |
| 41 | `Stock Market Wizards (Jack D. Schwager) (z-lib.org).pdf` | `Stock Market Wizards - Jack D. Schwager.pdf` | en | Z-Library .org | https://github.com/ametnes/nesis/issues/70 |
| 42 | `The Giver (Graphic Novel) by Lois Lowry, P. Craig Russell (z-lib.org).azw3` | `The Giver (Graphic Novel) - Lois Lowry & P. Craig Russell.azw3` | en | Z-Library .org, "by" | https://github.com/apprenticeharper/DeDRM_tools/issues/1609 |
| 43 | `Multiple Streams of Income Making Money in 2020 by Robert Allen (z-lib.org).azw3` | `Multiple Streams of Income Making Money in 2020 - Robert Allen.azw3` | en | Z-Library .org, "by" | https://github.com/apprenticeharper/DeDRM_tools/issues/1789 |
| 44 | `Python Crash Course, 2nd Edition A Hands-On, Project-Based Introduction to Programming by Eric Matthes (z-lib.org).pdf` | `Python Crash Course - Eric Matthes.pdf` | en | Z-Library .org + edition | https://github.com/shahwan42/drug-interaction-checker/issues/5 |
| 45 | `Lie Algebras in Particle Physics From... (z-lib.org).pdf` | `Lie Algebras in Particle Physics From.pdf` | en | Z-Library .org, cut | https://github.com/nbcongmingdan/stage_lpsc_2023/issues/1 |
| 46 | `Deep Learning by Ian Goodfellow, Yoshua Bengio, Aaron Courville (z-lib.org).pdf` | `Deep Learning - Ian Goodfellow et al.pdf` | en | Z-Library .org, "by" | https://github.com/Rishabh-creator601/Books |
| 47 | `GIS Tutorial for Health (z-lib.org).pdf` | `GIS Tutorial for Health.pdf` | en | Z-Library .org, no author | https://github.com/SammyGIS/GIS-Community-Resources |
| 48 | `Zombi Guía de Supervivencia (Brooks, Max) (z-lib.org).epub.pdf` | `Zombi Guía de Supervivencia - Max Brooks.pdf` | es | Z-Library .org, double extension | https://archive.org/download/zombi-guia-de-supervivencia-brooks-max-z-lib.org.epub/ |
| 49 | `Potentialities Collected Essays in Philosophy (Giorgio Agamben, Daniel Heller-Roazen) (Z-Library).pdf` | `Potentialities Collected Essays in Philosophy - Giorgio Agamben & Daniel Heller-Roazen.pdf` | en | (Z-Library) | https://github.com/chrisgrieser/alfred-quick-file-access/issues/3 |
| 50 | `The Technology Trap Capital, Labor, And Power In The Age Of Automation (Carl Benedikt Frey) (Z-Library).pdf` | `The Technology Trap Capital, Labor, And Power In The Age Of Automation - Carl Benedikt Frey.pdf` | en | (Z-Library) | https://github.com/chrisgrieser/alfred-quick-file-access/issues/3 |
| 51 | `The Emotional Life of the Toddler (Alicia F. Lieberman)—_Simon Schuster_English_9781476792033 (Z-Library).epub` | `The Emotional Life of the Toddler - Alicia F. Lieberman.epub` | en | (Z-Library) + publisher_language_ISBN | https://github.com/immersive-translate/immersive-translate/issues/3857 |
| 52 | `Barbarossa Derailed The German Advanve, Encirclement Battle, and the First and Second Soviet Counteroffensives, 10 July-24... (David Glantz) (Z-Library).epub` | `Barbarossa Derailed The German Advanve, Encirclement Battle, and the First and Second Soviet Counteroffensives, 10 July-24 - David Glantz.epub` | en | (Z-Library), cut | https://github.com/NEKOparapa/AiNiee/issues/224 |
| 53 | `Alcatraz Versus the Knights of Crystallia (Sanderson Brandon) (Z-Library).epub` | `Alcatraz Versus the Knights of Crystallia - Sanderson Brandon.epub` | en | (Z-Library), surname first | https://github.com/smoores-dev/storyteller/issues/9 |
| 54 | `book Blender Studio Projects Digital Movie-Making (Tony Mullen Claudio Andaur) (Z-Library).epub` | `book Blender Studio Projects Digital Movie-Making - Tony Mullen Claudio Andaur.epub` | en | (Z-Library), "book" prefix | https://github.com/sumatrapdfreader/sumatrapdf/issues/5100 |
| 55 | `(NEW)第二性：合卷本 = Le Deuxième Sexe（简体台版译本） (西蒙‧德‧波娃 (Simone de Beauvoir) 著；邱瑞鑾 譯) (Z-Library).epub` | `第二性.epub` | zh/fr | (Z-Library) | https://github.com/koreader/koreader/issues/12183 |
| 56 | `Scott Berkun - How Design Makes the World (2020, Berkun Media LLC) - libgen.li.epub` | `How Design Makes the World - Scott Berkun.epub` | en | libgen.li | https://github.com/koreader/koreader/issues/11111 |
| 57 | `Darius Foroux - Focus on What Matters_ A Collection of Stoic Letters on Living Well (2022, North Eagle Publishing) - libgen.li.epub` | `Focus on What Matters - Darius Foroux.epub` | en | libgen.li | https://github.com/Aryan-Raj3112/episteme/issues/244 |
| 58 | `Hirini Moko Mead - Tikanga Maori_ Living By Maori Values (2016, Huia Publishers) - libgen.li.epub` | `Tikanga Maori - Hirini Moko Mead.epub` | en | libgen.li | https://github.com/noDRM/DeDRM_tools/issues/194 |
| 59 | `Publishers, Tyndale House - Daily Walk Bible-NLT (1997, Tyndale House Publishers) - libgen.li.epub` | `Daily Walk Bible-NLT - Tyndale House Publishers.epub` | en | libgen.li, corporate author | https://github.com/AndBible/and-bible/issues/3591 |
| 60 | `Bill Browder - Red notice a tru - libgen.li.epub` | `Red notice a tru - Bill Browder.epub` | en | libgen.li, cut | https://github.com/cporcellijr/bookbridge/issues/290 |
| 61 | `Chronicles of Narnia - Publication order 4  C. S. Lewis - The Silver Chair Narnia 1994 - libgen.li.pdf` | `Chronicles of Narnia - Publication order 4 C. S. Lewis - The Silver Chair Narnia 1994.pdf` | en | libgen.li, series first | https://github.com/DrewThomasson/ebook2audiobook/issues/732 |
| 62 | `Guy de Maupassant - The Complete Short Stories by Guy de Maupassant--Delphi Classics (Illustrated) (2017, Delphi Classics (Parts Edition)) - libgen.li.epub` | `The Complete Short Stories - Guy de Maupassant.epub` | en | libgen.li, nested brackets | https://github.com/sumatrapdfreader/sumatrapdf/issues/5100 |
| 63 | `Harry Potter and the Sorcerer's Stone.libgen.li.epub` | `Harry Potter and the Sorcerer's Stone.epub` | en | libgen.li dotted | https://github.com/ScottBot10/calibre_annas_archive/issues/3 |
| 64 | `アメリカン・ブッダ (ハヤカワ文庫JA){柴田 勝家}(2020, 早川書房){115086913} libgen.li.epub` | `アメリカン・ブッダ (ハヤカワ文庫JA) - 柴田 勝家.epub` | ja | libgen.li braces | https://github.com/auto-novel/auto-novel/issues/332 |
| 65 | `Neil Shubin - Your Inner Fish_ A Journey into the 3.5-Billion-Year History of the Human Body (2008, Pantheon) - libgen.lc.epub` | `Your Inner Fish - Neil Shubin.epub` | en | libgen.lc | https://github.com/advokatb/KOReader-Patches/issues/1 |
| 66 | `Udaya Seneviratne - Making Sense of the EEG. From Basic Principles to Clinical Applications (2023, CRC Press) - libgen.li.pdf` | `Making Sense of the EEG. From Basic Principles to Clinical Applications - Udaya Seneviratne.pdf` | en | libgen.li | https://archive.org/download/udaya-seneviratne-making-sense-of-the-eeg.-from-basic-principles-to-clinical-app/ |
| 67 | `[Weird Horror _4] Michael Kelly - Weird Horror _4 (2022, Undertow Publications) - libgen.li.epub` | `Weird Horror 4 - Michael Kelly.epub` | en | libgen.li + [Series] | https://archive.org/download/weird-horror-4-michael-kelly-weird-horror-4-2022-undertow-publications-libgen.li/ |
| 68 | `[2019-7 2019-jul] - (2019) - libgen.li (1).pdf` | (no change) | ? | libgen.li magazine + (1) | https://archive.org/download/vanity-fair-italia-2021-42-2021-libgen.li/ |
| 69 | `_OceanofPDF.com_Finding_Yourself_for_Starseeds_and_Lightworkers_-_Gil_Sim-vi.pdf` | `Finding Yourself for Starseeds and Lightworkers - Gil Sim-vi.pdf` | en | OceanofPDF | https://github.com/breslee1707/VI-Translate/issues/19 |
| 70 | `_OceanofPDF.com_Adobe_Photoshop_Classroom_in_a_Book_2026_-_Conrad_Chavez.pdf` | `Adobe Photoshop Classroom in a Book 2026 - Conrad Chavez.pdf` | en | OceanofPDF | https://github.com/rnwilliamson91/AUTOMATE/issues/1 |
| 71 | `_OceanofPDF.com_Fright_Night_Origins_-_Tom_Holland_A_Jack_Ulrich.kepub` | `Fright Night Origins - Tom Holland A Jack Ulrich.kepub` | en | OceanofPDF (kepub) | https://github.com/kyxap/koreader-calibre-plugin/issues/79 |
| 72 | `_OceanofPDF.com_pharmaceutical_chemistry_-_david_g_watson.pdf` | `Pharmaceutical Chemistry - David G Watson.pdf` | en | OceanofPDF, lower case | https://archive.org/download/oceanof-pdf.com-pharmaceutical-chemistry-david-g-watson/ |
| 73 | `_OceanofPDF.com_Πειθώ_Greek_Edition_-_Jane_Austen.pdf` | `Πειθώ - Jane Austen.pdf` | el | OceanofPDF + Greek Edition | https://archive.org/download/oceanof-pdf.com-greek-edition-jane-austen/ |
| 74 | `_OceanofPDF.com_A_Beggar_in_Jerusalem_-_Elie_Wiesel.epub` | `A Beggar in Jerusalem - Elie Wiesel.epub` | en | OceanofPDF | https://archive.org/download/oceanof-pdf.com-books/ |
| 75 | `_OceanofPDF.com_Python_Machine_Learning_-_Sebastian_Raschka.pdf` | `Python Machine Learning - Sebastian Raschka.pdf` | en | OceanofPDF | https://github.com/harshasanthapur/ebooks |
| 76 | `Atomic habits ( PDFDrive ).pdf` | `Atomic habits.pdf` | en | PDFDrive | https://github.com/abhisheksingh9263136434-div/Avii-/issues/1 |
| 77 | `Atomic habits ( PDFDrive.com ).pdf` | `Atomic habits.pdf` | en | PDFDrive .com | https://github.com/NiharShrotri/llm-wiki/issues/7 |
| 78 | `OpenCV Computer Vision Projects with Python ( PDFDrive.com ).pdf` | `OpenCV Computer Vision Projects with Python.pdf` | en | PDFDrive .com | https://github.com/Rishabh-creator601/Books |
| 79 | `Mastering Bitcoin_ Programming the Open Blockchain (O'Reilly 2nd edition 2017) ( PDFDrive ).pdf` | `Mastering Bitcoin.pdf` | en | PDFDrive + edition | https://github.com/LearnWeb3DAO/Content-Contributions/issues/5 |
| 80 | `Roots_ The Saga of an American Family   ( PDFDrive ).pdf` | `Roots.pdf` | en | PDFDrive, extra spaces | https://archive.org/download/oceanof-pdf/ |
| 81 | `Empieza con el Porque - Simon Sinek ( PDFDrive ).pdf` | `Empieza con el Porque - Simon Sinek.pdf` | es | PDFDrive | https://github.com/David-Climaco/Estrategia-/issues/1 |
| 82 | `Como não aprender Inglês ( PDFDrive ).pdf` | `Como não aprender Inglês.pdf` | pt | PDFDrive | https://github.com/bepronetwork/bepro-js-edge/issues/2568 |
| 83 | `Mi historia (Spanish Edition).azw3` | `Mi historia.azw3` | es | Kindle (Spanish Edition) | https://github.com/apprenticeharper/DeDRM_tools/issues/797 |
| 84 | `El vendedor de silencio (Spanish Edition).azw3` | `El vendedor de silencio.azw3` | es | Kindle (Spanish Edition) | https://github.com/apprenticeharper/DeDRM_tools/issues/916 |
| 85 | `Una propuesta para humillar a España (Spanish Edition)_B086DQNSXS.kfx` | `Una propuesta para humillar a España.kfx` | es | Kindle + ASIN | https://github.com/apprenticeharper/DeDRM_tools/issues/2052 |
| 86 | `Gestão da mudança e cultura organizacional (FGV Management) (Portuguese Edition)_B01DWE9EMK.kfx` | `Gestão da mudança e cultura organizacional (FGV Management).kfx` | pt | Kindle + ASIN | https://github.com/apprenticeharper/DeDRM_tools/issues/1087 |
| 87 | `Weis, Margaret & Hickman, Tracy - [La rosa del profeta 2] El paladin de la noche (r1.0).epub` | `El paladin de la noche - Margaret Weis & Tracy Hickman.epub` | es | ePubLibre | https://github.com/thegooglecodearchive/eplvalidator/issues/13 |
| 88 | `Ussia Munoz-Seca, Alfonso - [Sotoancho 07] Las canicas, las cuquis y el novio tontito de Mama (r1.0).epub` | `Las canicas, las cuquis y el novio tontito de Mama - Alfonso Ussia Munoz-Seca.epub` | es | ePubLibre | https://github.com/thegooglecodearchive/eplvalidator/issues/12 |
| 89 | `[Mundo de tinieblas] [Cazador y presa 2] Fleming, Gherbod - Juez (r1.0).epub` | `Juez - Gherbod Fleming.epub` | es | ePubLibre, series first | https://github.com/thegooglecodearchive/eplvalidator/issues/10 |
| 90 | `Tuchman, Barbara W. - Los cañones de agosto [EPL-FS] [1962] [2019].pdf` | `Los cañones de agosto - Barbara W. Tuchman.pdf` | es | ePubLibre facsimile | https://archive.org/download/tuchman-barbara-w.-los-canones-de-agosto-epl-fs-1962-2019/ |
| 91 | `Ceniza, somos ceniza__ - Clark Carrados.epub` | `Ceniza, somos ceniza - Clark Carrados.epub` | es | Calibre "Title - Author", __ | https://archive.org/download/CenizaSomosCenizaClarkCarrados/ |
| 92 | `ANIMAL FARM - George Orwell.epub` | `Animal Farm - George Orwell.epub` | en | all caps | https://github.com/koreader/koreader/issues/4763 |
| 93 | `jamie bartlett - the dark net.epub` | `Jamie Bartlett - The Dark Net.epub` | en | all lower case | https://github.com/koreader/koreader/issues/14215 |
| 94 | `Babel_ Or the Necessity of Violence_ An Arcane History of the Oxford Translators' Revolution - R. F. Kuang (10).epub` | `Babel - R. F. Kuang.epub` | en | Calibre id suffix | https://github.com/koreader/koreader/issues/13649 |
| 95 | `Abir Mukherjee - [Sam Wyndham 001] A Rising Man.epub` | (no change) | en | Calibre series | https://github.com/koreader/koreader/issues/11515 |
| 96 | `Nate Silver - [ ] La senal y el ruido.epub` | `Nate Silver - La senal y el ruido.epub` | es | empty series | https://github.com/koreader/koreader/issues/11515 |
| 97 | `A Fazenda Pública em Juízo - Leonardo Carneiro da Cunha - 2017.epub` | `A Fazenda Pública em Juízo - Leonardo Carneiro da Cunha.epub` | pt | year part | https://github.com/koreader/koreader/issues/3153 |
| 98 | `[w-id_7782082] An Island To Oneself - Suvarov.epub` | `An Island To Oneself - Suvarov.epub` | en | id prefix | https://github.com/koreader/koreader/issues/4340 |
| 99 | `BARACK-OBAMA-UNE-TERRE-PROMISE.pdf` | `Barack Obama Une Terre Promise.pdf` | fr | slug, all caps | https://github.com/koreader/koreader/issues/8053 |
| 100 | `Kimball_The-Data-Warehouse-Toolkit-3rd-Edition.pdf` | `Kimball The Data Warehouse Toolkit.pdf` | en | slug | https://github.com/koreader/koreader/issues/10875 |
| 101 | `Programming.Challenges-2003.pdf` | `Programming Challenges.pdf` | en | dotted + year | https://github.com/koreader/koreader/issues/663 |
| 102 | `alexandre-dumas_the-count-of-monte-cristo_chapman-and-hall.epub` | `The Count of Monte Cristo - Alexandre Dumas.epub` | en | Standard Ebooks slug | https://github.com/koreader/koreader/issues/10678 |
| 103 | `Caitlin Doughty - Will My Cat Eat My Eyeballs__ Big Questions from Tiny Mortals About Death.epub` | `Caitlin Doughty - Will My Cat Eat My Eyeballs.epub` | en | Calibre, __ | https://github.com/koreader/koreader/issues/6203 |
| 104 | `1Q84 - Haruki Murakami.kepub.epub` | `1Q84 - Haruki Murakami.epub` | en | double extension | https://github.com/koreader/koreader/issues/14456 |
| 105 | `Robert Maurer Ph.D. - One Small Step Can Change Your Life_ The Kaizen Way-Workman Publishing Company (2004).epub` | `Robert Maurer - One Small Step Can Change Your Life.epub` | en | credential, publisher, year | https://github.com/koreader/koreader/issues/6447 |
| 106 | `como_criar_um_leitor_v2 (1).epub` | `Como Criar um Leitor.epub` | pt | underscores, v2, (1) | https://archive.org/download/como-criar-um-leitor-v-2-1/ |
| 107 | `La genealogia de la moral - Friedrich Nietzsche (1).pdf` | `La genealogia de la moral - Friedrich Nietzsche.pdf` | es | (1) | https://archive.org/download/la-genealogia-de-la-moral-friedrich-nietzsche-1/ |
| 108 | `FF001_Overdrive_UK_v1.0.epub` | `FF001 Overdrive UK.epub` | en | underscores, v1.0 | https://github.com/w3c/epubcheck/issues/1298 |
| 109 | `05 FULGRIM -Herejía de Horus_lcp.epub` | `05 Fulgrim - Herejía de Horus.epub` | es | _lcp | https://archive.org/download/w40k_esp_biblioteca/ |
| 110 | `Kipling, R.-El Hombre que pudo reinar, Madrid, 2016. 67 pp..epub` | `Kipling, R.-El Hombre que pudo reinar, Madrid, 2016. 67 pp.epub` | es | uploader notes | https://archive.org/download/kipling-r.-el-hombre-que-pudo-reinar-madrid-2016./ |
| 111 | `alcoforado, soro Mariana - Cartas-de-Amor-de-uma-Freira-Portuguesa.epub` | `alcoforado, soro Mariana - Cartas de Amor de uma Freira Portuguesa.epub` | pt | slug part | https://archive.org/download/cartas-de-amor-de-uma-freira-portuguesa-lusolivros/ |
| 112 | `isbn_9780847831470_lcp.epub` | (no change) | ? | ISBN + _lcp | https://github.com/noDRM/DeDRM_tools/issues/490 |
