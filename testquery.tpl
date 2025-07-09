{{ define "query" }}
    SELECT
        100                                    {{ Scan.Int.To "Int" }}
        , 'value'                              {{ Scan.String.To "String" }}
        , true                                 {{ Scan.To "Bool" }}
        , '2000-12-31'                         {{ (Scan.String.ParseTime DateOnly).To "Time" }}
        , '300'                                {{ Scan.Text.To "Big" }}
        , 'https://example.com/path?query=yes' {{ Scan.Binary.To "URL" }}
        , 'hello,world'                        {{ (Scan.String.Split ",").To "Slice" }}
        , '{"hello":"world"}'                  {{ Scan.JSON.To "JSON" }}
{{ end }}
