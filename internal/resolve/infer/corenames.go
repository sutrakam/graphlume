package infer

import "strings"

// # Member names a language's own library declares
//
// memberByName binds `x.f()` on an `x` of unknown type when exactly one type in
// the repository declares an `f`. That uniqueness is evidence only if the
// repository is where an `f` would be declared — and for the names below it is
// not. Every string has a `toString`, every Rust collection a `clone`, every
// Ruby object a `to_s`. A repository that happens to declare exactly one of
// those says nothing about what `issue.maximum.toString()` reaches: the other
// declarations are in a library that was never indexed. This is A20's
// argument — uniqueness can be a property of the index's boundary rather than
// of the program — applied to members.
//
// Measured before this list existed, on the "only member of this name" rule:
// zod, 3 right of 14 sampled, eleven of the wrong ones `toString`, `includes`
// and `bigint` on built-in values; ripgrep, 4 right of 8, the wrong ones
// `as_str`, `as_path`, `is_symlink` and `clone`.
//
// The lists are the method names of each language's core types — strings,
// numbers, collections, the root object — and nothing wider. They are not a
// model of a standard library and are not meant to grow into one: a name here
// costs a correct binding wherever a repository's own type declares it and is
// called on a receiver whose type the source does not state. That call is
// recorded ambiguous, which is what it is.
//
// Go has no list. A Go value's methods are declared by a package — this
// repository's or an imported one — and never by the language, and the rule
// measured 99.3% on gin (458 of 461, against scip-go) without one.

// coreMembers maps a language to the member names its core library declares.
// Built once, read-only afterwards.
var coreMembers = buildCoreMembers()

func buildCoreMembers() map[string]map[string]struct{} {
	coreMembers := make(map[string]map[string]struct{}, 24)
	script := words(`
		toString valueOf toJSON toLocaleString hasOwnProperty isPrototypeOf
		includes indexOf lastIndexOf slice splice join split trim trimStart trimEnd
		replace replaceAll match matchAll test exec search at charAt charCodeAt codePointAt
		substring substr padStart padEnd repeat startsWith endsWith toLowerCase toUpperCase
		normalize localeCompare concat push pop shift unshift reverse sort fill flat flatMap
		map filter reduce reduceRight forEach some every find findIndex findLast findLastIndex
		keys values entries has get set add delete clear size then catch finally bind call apply
		toFixed toPrecision toExponential bigint getTime getFullYear getMonth getDate getHours
		toISOString toDateString next return throw copyWithin subarray isArray from of assign
		freeze resolve reject all race any now parse stringify log warn error info debug`)
	// The platform's other everyday types: fetch's Request, Response and
	// Headers (`res.clone()`, `req.json()`), Blob and FormData, DOM events and
	// AbortController, and Node's EventEmitter. Measured on hono, three of the
	// four wrong `medium` edges were `clone()` on a standard Request or Response
	// bound to the one `clone` the repository declares.
	script = merge(script, words(`
		clone text json arrayBuffer blob formData getAll abort addEventListener
		removeEventListener dispatchEvent preventDefault stopPropagation
		emit once addListener removeListener removeAllListeners`))
	for _, lang := range []string{"javascript", "typescript", "tsx", "vue", "svelte", "astro"} {
		coreMembers[lang] = script
	}

	jvm := words(`
		toString equals hashCode getClass compareTo clone finalize notify notifyAll wait
		length charAt substring indexOf lastIndexOf contains startsWith endsWith isEmpty isBlank
		trim strip split join replace replaceAll matches format valueOf toLowerCase toUpperCase
		toCharArray getBytes repeat concat intern lines chars
		add addAll remove removeAll removeIf retainAll clear size get set put putAll putIfAbsent
		containsKey containsValue keySet values entrySet getOrDefault computeIfAbsent merge
		iterator hasNext next forEach stream toArray sort reverse subList of copyOf
		map filter collect reduce findFirst findAny anyMatch allMatch noneMatch flatMap limit
		skip distinct sorted count min max orElse orElseThrow orElseGet isPresent ifPresent
		append insert delete deleteCharAt setLength capacity intValue longValue doubleValue
		floatValue shortValue byteValue booleanValue charValue parseInt parseLong parseDouble
		name ordinal getName getMessage getCause printStackTrace close flush read write run
		start interrupt sleep currentThread getKey getValue setValue peek push pop poll offer
		first last clear format onBackPressed`)
	for _, lang := range []string{"java", "scala", "groovy"} {
		coreMembers[lang] = jvm
	}

	// Kotlin has the JVM's names and its own standard library on top: the
	// number conversions, the scope functions and the collection and string
	// extensions. `use` is left out on purpose: okio, which targets platforms
	// without kotlin.io, declares its own Closeable.use, and 211 high-tier
	// calls to it were right. Measured on okio, 608 edges (of 1500 at the low tier) bound
	// `x.toLong()` to the one private `LARGE_INTEGER.toLong()` the repository
	// declares, and 11 of 30 sampled low edges were wrong, nine of them that.
	coreMembers["kotlin"] = merge(jvm, words(`
		toInt toLong toShort toByte toChar toDouble toFloat toUInt toULong toUShort toUByte
		toList toMutableList toSet toMutableSet toMap toTypedArray toByteArray toCharArray
		toIntArray toLongArray toSortedSet toBoolean toBooleanStrict toIntOrNull toLongOrNull
		toDoubleOrNull toRegex toPair
		let also apply run takeIf takeUnless
		lowercase uppercase lowercaseChar uppercaseChar replaceFirst substringBefore
		substringAfter substringBeforeLast substringAfterLast removePrefix removeSuffix
		removeRange isNullOrEmpty isNullOrBlank isNotEmpty isNotBlank orEmpty ifEmpty ifBlank
		padStart padEnd trimStart trimEnd trimIndent trimMargin lineSequence
		copyOf copyOfRange getOrNull getOrElse getOrPut firstOrNull
		lastOrNull singleOrNull elementAt elementAtOrNull indices lastIndex
		joinToString forEachIndexed mapIndexed mapNotNull filterNot filterIsInstance
		filterNotNull flatten associate associateBy associateWith groupBy partition zip
		sortedBy sortedWith sortedDescending sortBy sortWith minOrNull maxOrNull minBy maxBy
		sumOf fold foldRight onEach takeWhile dropWhile takeLast dropLast chunked windowed
		asSequence asList asIterable coerceAtLeast coerceAtMost coerceIn isNaN isInfinite
		isDigit isLetter isLetterOrDigit isWhitespace isUpperCase isLowerCase digitToInt
		require check error requireNotNull checkNotNull assert`))

	coreMembers["ruby"] = words(`
		to_s to_i to_f to_a to_h to_sym to_str to_proc to_json inspect class is_a? kind_of?
		instance_of? respond_to? send public_send method methods nil? empty? frozen? freeze dup
		clone tap then itself hash eql? equal? instance_variable_get instance_variable_set
		instance_variables define_method extend include? each each_with_index each_with_object
		map flat_map collect select filter reject find detect find_all reduce inject sum count
		size length first last take drop sort sort_by min max min_by max_by group_by partition
		zip uniq compact flatten reverse join split strip chomp chop sub gsub tr upcase downcase
		capitalize start_with? end_with? match match? scan index rindex slice push pop shift
		unshift append prepend insert delete delete_if delete_at clear concat key? has_key?
		value? keys values fetch dig merge merge! update store any? all? none? one? times upto
		downto step call new lock unlock synchronize join raise puts print p format
		include extend prepend require require_relative load attr_reader attr_writer
		attr_accessor private public protected module_function alias_method instance_eval
		class_eval module_eval define_singleton_method bytesize bytes force_encoding
		encode unpack unpack1 pack escape unescape chars lines ljust rjust
		center rewind eof? getc gets readline readlines binmode sync close_write close_read
		must_equal must_be_nil must_be must_include must_match must_raise
		wont_equal wont_be_nil wont_include`)

	coreMembers["rust"] = words(`
		clone to_string to_owned into from as_ref as_mut borrow borrow_mut deref eq ne cmp
		partial_cmp hash fmt default drop unwrap unwrap_or unwrap_or_else unwrap_or_default
		expect ok err ok_or ok_or_else map map_err map_or map_or_else and_then or_else and or
		is_some is_none is_ok is_err take replace get_or_insert_with as_deref iter iter_mut
		into_iter next peekable collect filter filter_map flat_map fold for_each enumerate zip
		rev skip chain any all find position count sum last nth cloned copied len is_empty push
		pop insert remove get get_mut contains contains_key keys values entry extend clear
		truncate drain retain sort sort_by sort_unstable dedup first split join trim
		trim_start trim_end starts_with ends_with as_str as_bytes as_slice as_path as_os_str
		to_str to_path_buf to_vec to_lowercase to_uppercase to_string_lossy chars bytes lines
		parse write write_all write_fmt flush read read_to_string read_line lock unwrap_err
		display exists is_file is_dir is_symlink file_name file_type extension parent metadata
		path with_extension strip_prefix components canonicalize`)

	coreMembers["c_sharp"] = words(`
		ToString Equals GetHashCode GetType CompareTo Clone Dispose Add AddRange Remove RemoveAt
		RemoveAll Clear Contains ContainsKey ContainsValue TryGetValue Insert IndexOf LastIndexOf
		Count Length ToArray ToList ToDictionary First FirstOrDefault Last LastOrDefault Single
		SingleOrDefault Where Select SelectMany Any All OrderBy OrderByDescending GroupBy Sum Min
		Max Average Aggregate Distinct Skip Take Concat Reverse Sort ForEach Find FindAll Exists
		GetEnumerator MoveNext Reset Substring Split Join Trim TrimStart TrimEnd Replace
		StartsWith EndsWith ToLower ToUpper ToLowerInvariant ToUpperInvariant PadLeft PadRight
		Format Append AppendLine AppendFormat Parse TryParse Write WriteLine Read ReadLine Flush
		Close GetValue SetValue Invoke CopyTo AsEnumerable Cast OfType HasValue GetValueOrDefault
		AsSpan AsMemory Slice Fill`)

	coreMembers["cpp"] = words(`
		size length empty clear begin end cbegin cend rbegin rend front back data c_str at
		push_back pop_back emplace_back emplace insert erase find count substr append assign
		compare resize reserve capacity swap get reset release str first second lock unlock
		wait notify_one notify_all join detach load store exchange what open close read write
		flush good eof fail`)
	coreMembers["c"] = coreMembers["cpp"]

	coreMembers["python"] = words(`
		append extend insert remove pop clear index count sort reverse copy keys values items
		get setdefault update popitem add discard union intersection difference join split
		rsplit strip lstrip rstrip replace format startswith endswith lower upper title
		capitalize encode decode find rfind isdigit isalpha isspace zfill splitlines partition
		read write readline readlines close flush seek tell
		start stop is_alive isatty fileno readable writable seekable truncate getvalue
		detach wait poll communicate terminate kill send recv sendall accept bind listen
		connect mkdir exists is_file is_dir stat glob rglob iterdir read_text write_text
		read_bytes write_bytes unlink rmdir touch readinto read1 peek acquire release
		notify notify_all is_set cancel submit shutdown put put_nowait get_nowait
		task_done qsize search group groups span findall finditer fullmatch dumps loads
		dump load readouterr raises setattr setenv delenv delattr chdir
		setUp tearDown setUpClass tearDownClass skipTest subTest addCleanup fail
		json execute executemany commit rollback cursor fetchone fetchall fetchmany
		scalars scalar scalar_one scalar_one_or_none where select post patch
		add_column drop_column`)

	// PHP: the language's own interfaces and classes, then the two things
	// nearly every PHP repository calls into without declaring: PSR-7/17/18
	// (the HTTP message, factory and client interfaces, which frameworks and
	// libraries both implement and take as arguments) and PHPUnit. Measured on guzzle, 27 of 30 sampled low edges were wrong, and
	// every one of the 27 was a PSR-7 method (`getBody`, `getHeaderLine`,
	// `getUri`, `hasHeader`, `withHeader`, `getHost`, `detach`, `getContents`)
	// or PHPUnit's `fail`, bound to a test double or a private helper that
	// happens to declare the one other method of the name. PHPUnit's
	// assert*/expect*/markTest* families are matched by prefix (corePrefixes).
	coreMembers["php"] = words(`
		__toString __construct __invoke __get __set __isset __unset __call __callStatic __clone
		__destruct __debugInfo __serialize __unserialize __set_state
		count getMessage getCode getPrevious getFile getLine getTrace getTraceAsString
		current key next rewind valid send getReturn offsetGet offsetSet offsetExists offsetUnset
		jsonSerialize getTimestamp setTimezone bindTo fromCallable
		setAccessible newInstanceArgs newInstanceWithoutConstructor
		getMethod getProtocolVersion withProtocolVersion getHeaders hasHeader getHeader getHeaderLine
		withHeader withAddedHeader withoutHeader getBody withBody withMethod getUri
		withUri getRequestTarget withRequestTarget getStatusCode withStatus getReasonPhrase
		getServerParams getCookieParams withCookieParams getQueryParams withQueryParams
		getUploadedFiles withUploadedFiles getParsedBody withParsedBody getAttributes getAttribute
		withAttribute withoutAttribute
		close detach getSize tell eof isSeekable seek isWritable write isReadable read
		getContents getMetadata
		getScheme getAuthority getUserInfo getHost getPort getPath getQuery getFragment
		withScheme withUserInfo withHost withPort withPath withQuery withFragment
		getStream moveTo getError getClientFilename getClientMediaType
		createRequest createServerRequest createResponse createStream createStreamFromFile
		createStreamFromResource createUploadedFile createUri sendRequest
		fail setUp tearDown setUpBeforeClass tearDownAfterClass createMock createStub
		createConfiguredMock createPartialMock getMockBuilder addToAssertionCount
		once never atLeastOnce atLeast exactly willReturn willReturnCallback
		willReturnSelf willReturnMap willReturnOnConsecutiveCalls willThrowException
		returnValue returnCallback withConsecutive onlyMethods disableOriginalConstructor
		with expects method will
		where whereIn whereNull whereNotNull orWhere orderBy groupBy select
		first firstOrFail find findOrFail pluck paginate simplePaginate
		exists doesntExist
		getMock`)
	coreMembers["swift"] = words(`
		append insert remove removeAll contains count isEmpty first last map filter reduce
		forEach sorted sort joined split hasPrefix hasSuffix lowercased uppercased description`)
	return coreMembers
}

// merge is the union of several name sets, as a new set.
func merge(sets ...map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{})
	for _, set := range sets {
		for k := range set {
			out[k] = struct{}{}
		}
	}
	return out
}

func words(s string) map[string]struct{} {
	fields := strings.Fields(s)
	out := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		out[f] = struct{}{}
	}
	return out
}

// isCoreMember reports whether a language's own core library declares a
// member of this name, so that a repository declaring exactly one says nothing
// about what a call on an unknown receiver reaches.
func (s LanguageSpec) isCoreMember(name string) bool {
	if _, ok := coreMembers[s.Name][name]; ok {
		return true
	}
	for _, p := range corePrefixes[s.Name] {
		// A family, not a word: assertSame, assertCount, assertStringContainsString.
		// The letter after the prefix is a capital, so `assertion` is not one.
		if len(name) > len(p) && strings.HasPrefix(name, p) && (strings.HasSuffix(p, "_") || name[len(p)] >= 'A' && name[len(p)] <= 'Z') {
			return true
		}
	}
	return false
}

// corePrefixes are families of member names a language's dominant test
// framework declares by the dozen. PHPUnit's TestCase has some hundred
// assert* methods and the expect* and markTest* groups; listing them by name
// would be a list nobody could keep current.
var corePrefixes = map[string][]string{
	"php": {"assert", "expect", "markTest"},
	// unittest's TestCase and unittest.mock.
	"python": {"assert", "assert_"},
	// Minitest: `assert_equal`, `refute_nil`.
	"ruby": {"assert_", "refute_"},
}
